package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	api, email, password, visibility string
	statePath, overpass, userAgent   string
	regions                          []BBox
	routes                           []string
	interval                         time.Duration
	limit                            int
	dryRun                           bool
	rules                            Rules
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for {
		start := time.Now()
		sum, err := runOnce(ctx, cfg, log)
		log.Info("run finished", append(sum.attrs(), "duration", time.Since(start).Round(time.Second).String())...)
		if err != nil && ctx.Err() == nil {
			log.Error("run failed", "error", err)
			if cfg.interval == 0 {
				os.Exit(1)
			}
		}
		if cfg.interval == 0 || sleep(ctx, cfg.interval) != nil {
			return
		}
	}
}

func parseFlags(args []string) (config, error) {
	fs := flag.NewFlagSet("scraper", flag.ContinueOnError)
	c := config{rules: DefaultRules()}
	var regions, routes string
	fs.StringVar(&c.api, "api", env("SCRAPER_API", "http://localhost:8080"), "Travel Docs API base URL")
	fs.StringVar(&c.email, "email", env("SCRAPER_EMAIL", "scraper@example.com"), "account that owns the exported routes (registered if missing)")
	fs.StringVar(&c.password, "password", env("SCRAPER_PASSWORD", "scraper-password"), "its password")
	fs.StringVar(&c.visibility, "visibility", env("SCRAPER_VISIBILITY", "public"), "visibility of new routes: public or private")
	fs.StringVar(&c.statePath, "state", env("SCRAPER_STATE", "data/state.json"), "state file (what was exported)")
	fs.StringVar(&regions, "regions", env("SCRAPER_REGIONS", "46.45,7.75,46.7,8.1"), "areas to scan: south,west,north,east; several separated by ';'")
	fs.StringVar(&routes, "routes", env("SCRAPER_ROUTES", "hiking,bicycle"), "OSM route types to collect")
	fs.StringVar(&c.overpass, "overpass", env("SCRAPER_OVERPASS", "https://overpass-api.de/api/interpreter"), "Overpass API endpoint")
	fs.StringVar(&c.userAgent, "user-agent", env("SCRAPER_USER_AGENT", "route-scraper/0.1"), "User-Agent for sources; add contact details for heavy use")
	interval, err := time.ParseDuration(env("SCRAPER_INTERVAL", "0s"))
	if err != nil {
		return c, fmt.Errorf("SCRAPER_INTERVAL: %w", err)
	}
	limit, err := strconv.Atoi(env("SCRAPER_LIMIT", "0"))
	if err != nil {
		return c, fmt.Errorf("SCRAPER_LIMIT: %w", err)
	}
	fs.DurationVar(&c.interval, "interval", interval, "run again after this long (e.g. 24h); 0 runs once")
	fs.IntVar(&c.limit, "limit", limit, "export at most this many routes per run (0: no limit)")
	fs.BoolVar(&c.dryRun, "dry-run", false, "discover and validate only; export nothing")
	fs.Float64Var(&c.rules.MinLengthM, "min-length-m", c.rules.MinLengthM, "skip shorter routes")
	fs.Float64Var(&c.rules.MaxLengthM, "max-length-m", c.rules.MaxLengthM, "skip longer routes")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	for _, r := range strings.Split(regions, ";") {
		if r = strings.TrimSpace(r); r == "" {
			continue
		}
		b, err := ParseBBox(r)
		if err != nil {
			return c, err
		}
		c.regions = append(c.regions, b)
	}
	if len(c.regions) == 0 {
		return c, fmt.Errorf("no regions to scan")
	}
	for _, r := range strings.Split(routes, ",") {
		if r = strings.TrimSpace(r); r != "" {
			if strings.ContainsAny(r, `"\|^$()`) {
				return c, fmt.Errorf("invalid route type %q", r)
			}
			c.routes = append(c.routes, r)
		}
	}
	if c.visibility != "public" && c.visibility != "private" {
		return c, fmt.Errorf("visibility must be public or private")
	}
	return c, nil
}

// summary counts what happened in one run.
type summary struct {
	Found, New, Updated, Unchanged, Failed int
	Rejected                               map[string]int
}

func (s summary) attrs() []any {
	a := []any{"found", s.Found, "new", s.New, "updated", s.Updated, "unchanged", s.Unchanged, "failed", s.Failed}
	reasons := make([]string, 0, len(s.Rejected))
	for r := range s.Rejected {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		a = append(a, "rejected: "+r, s.Rejected[r])
	}
	return a
}

// runOnce scans every source, validates and exports what is new or changed.
func runOnce(ctx context.Context, cfg config, log *slog.Logger) (summary, error) {
	sum := summary{Rejected: map[string]int{}}
	hc := &http.Client{Timeout: 4 * time.Minute}
	sources := []Source{&OSMSource{Endpoint: cfg.overpass, Regions: cfg.regions, Routes: cfg.routes, UserAgent: cfg.userAgent, HTTP: hc}}

	state, err := LoadState(cfg.statePath)
	if err != nil {
		return sum, fmt.Errorf("load state: %w", err)
	}
	x := &Exporter{Base: strings.TrimRight(cfg.api, "/"), Email: cfg.email, Password: cfg.password, Visibility: cfg.visibility, HTTP: hc}
	if !cfg.dryRun {
		if err := x.Login(ctx); err != nil {
			return sum, err
		}
	}

	exported := 0
	for _, src := range sources {
		tracks, err := src.Tracks(ctx)
		if err != nil {
			return sum, fmt.Errorf("source %s: %w", src.Name(), err)
		}
		log.Info("source scanned", "source", src.Name(), "tracks", len(tracks))
		for _, t := range tracks {
			sum.Found++
			rec := state.Records[t.SourceID]
			if reason := cfg.rules.Validate(&t); reason != "" {
				sum.Rejected[reason]++
				if rec == nil {
					state.Records[t.SourceID] = &Record{Rejected: reason, UpdatedAt: time.Now().UTC()}
				}
				continue
			}
			fp := t.Fingerprint()
			if rec != nil && rec.RouteID != "" && rec.Fingerprint == fp {
				sum.Unchanged++
				continue
			}
			if cfg.dryRun {
				log.Info("would export", "source_id", t.SourceID, "name", t.Name, "km", int(t.LengthM()/1000))
				continue
			}
			if cfg.limit > 0 && exported >= cfg.limit {
				continue
			}
			prev := ""
			if rec != nil {
				prev = rec.RouteID
			}
			routeID, err := x.Export(ctx, t, prev)
			if err != nil {
				if ctx.Err() != nil {
					return sum, ctx.Err()
				}
				sum.Failed++
				log.Warn("export failed", "source_id", t.SourceID, "name", t.Name, "error", err)
				continue
			}
			exported++
			if prev != "" && prev == routeID {
				sum.Updated++
			} else {
				sum.New++
			}
			state.Records[t.SourceID] = &Record{RouteID: routeID, Fingerprint: fp, UpdatedAt: time.Now().UTC()}
			// Saved after every export so an interrupted run never repeats work.
			if err := state.Save(); err != nil {
				return sum, fmt.Errorf("save state: %w", err)
			}
			log.Info("exported", "source_id", t.SourceID, "name", t.Name, "route_id", routeID, "updated", prev != "")
		}
	}
	if cfg.dryRun {
		return sum, nil
	}
	return sum, state.Save()
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
