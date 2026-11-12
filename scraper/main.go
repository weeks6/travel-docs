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
	regions                          []Region
	regionsPerRun                    int
	sources                          []string
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
	var regions, routes, sources string
	fs.StringVar(&c.api, "api", env("SCRAPER_API", "http://localhost:8080"), "Travel Docs API base URL")
	fs.StringVar(&c.email, "email", env("SCRAPER_EMAIL", "scraper@example.com"), "account that owns the exported routes (registered if missing)")
	fs.StringVar(&c.password, "password", env("SCRAPER_PASSWORD", "scraper-password"), "its password")
	fs.StringVar(&c.visibility, "visibility", env("SCRAPER_VISIBILITY", "public"), "visibility of new routes: public or private")
	fs.StringVar(&c.statePath, "state", env("SCRAPER_STATE", "data/state.json"), "state file (what was exported)")
	fs.StringVar(&regions, "regions", env("SCRAPER_REGIONS", "world"),
		"areas to scan, separated by ';': world, a continent, a region name, or south,west,north,east.\nNames: world, "+regionNames())
	fs.StringVar(&sources, "sources", env("SCRAPER_SOURCES", sourceNames()),
		"sources to use, separated by ','; official datasets cover their own country only")
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
	perRun, err := strconv.Atoi(env("SCRAPER_REGIONS_PER_RUN", "6"))
	if err != nil {
		return c, fmt.Errorf("SCRAPER_REGIONS_PER_RUN: %w", err)
	}
	fs.IntVar(&c.regionsPerRun, "regions-per-run", perRun, "scan this many regions per run, continuing where the last run stopped (0: all)")
	fs.DurationVar(&c.interval, "interval", interval, "run again after this long (e.g. 24h); 0 runs once")
	fs.IntVar(&c.limit, "limit", limit, "export at most this many routes per run (0: no limit)")
	fs.BoolVar(&c.dryRun, "dry-run", false, "discover and validate only; export nothing")
	fs.Float64Var(&c.rules.MinLengthM, "min-length-m", c.rules.MinLengthM, "skip shorter routes")
	fs.Float64Var(&c.rules.MaxLengthM, "max-length-m", c.rules.MaxLengthM, "skip longer routes")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if c.regions, err = ParseRegions(regions); err != nil {
		return c, err
	}
	for _, src := range strings.Split(sources, ",") {
		if src = strings.TrimSpace(src); src == "" {
			continue
		}
		if _, err := datasetByID(src); err != nil && src != "osm" {
			return c, err
		}
		c.sources = append(c.sources, src)
	}
	if len(c.sources) == 0 {
		return c, fmt.Errorf("no sources")
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
	Regions                                []string
	Found, New, Updated, Unchanged, Failed int
	Rejected                               map[string]int
	SourceErrors                           int
}

func (s summary) attrs() []any {
	a := []any{"regions", strings.Join(s.Regions, ","), "found", s.Found, "new", s.New, "updated", s.Updated,
		"unchanged", s.Unchanged, "failed", s.Failed, "source_errors", s.SourceErrors}
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

// sources builds the configured sources. Official datasets come before
// OpenStreetMap, so when both have a trail, the official one is kept.
func (cfg config) buildSources(hc *http.Client) []Source {
	var official, osm []Source
	for _, id := range cfg.sources {
		if id == "osm" {
			osm = append(osm, &OSMSource{Endpoint: cfg.overpass, Routes: cfg.routes, UserAgent: cfg.userAgent, HTTP: hc})
			continue
		}
		d, _ := datasetByID(id) // checked by parseFlags
		official = append(official, &ArcGISSource{Dataset: d, UserAgent: cfg.userAgent, HTTP: hc})
	}
	return append(official, osm...)
}

// runOnce scans this run's regions with every source, validates the
// tracks and exports what is new or changed. Regions take turns: each run
// continues where the last one stopped (state.Cursor). A run that hits the
// export limit stops inside a region, and the next run resumes there.
func runOnce(ctx context.Context, cfg config, log *slog.Logger) (summary, error) {
	sum := summary{Rejected: map[string]int{}}
	hc := &http.Client{Timeout: 4 * time.Minute}
	sources := cfg.buildSources(hc)

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

	n := len(cfg.regions)
	count := n
	if cfg.regionsPerRun > 0 && cfg.regionsPerRun < n {
		count = cfg.regionsPerRun
	}
	cursor := state.Cursor % n
	exported := 0
	seen := map[string]bool{} // routes crossing two regions
	for i := range count {
		idx := (cursor + i) % n
		region := cfg.regions[idx]
		sum.Regions = append(sum.Regions, region.Name)
		limited := false
		for _, src := range sources {
			tracks, err := src.Tracks(ctx, region)
			if err != nil {
				if ctx.Err() != nil {
					return sum, ctx.Err()
				}
				// One source being down doesn't stop the others; the region
				// comes round again.
				sum.SourceErrors++
				log.Warn("source failed", "source", src.Name(), "region", region.Name, "error", err)
				continue
			}
			if len(tracks) > 0 {
				log.Info("source scanned", "source", src.Name(), "region", region.Name, "tracks", len(tracks))
			}
			for _, t := range tracks {
				if seen[t.SourceID] {
					continue
				}
				seen[t.SourceID] = true
				sum.Found++
				canExport := cfg.limit <= 0 || exported < cfg.limit
				out, err := process(ctx, cfg, log, state, x, t, &sum, canExport)
				if err != nil {
					return sum, err
				}
				switch out {
				case sent:
					exported++
				case deferred:
					limited = true
				}
			}
		}
		if limited {
			log.Info("export limit reached; the next run continues in this region", "region", region.Name)
			break
		}
		// This region is done: the next run starts after it.
		state.Cursor = (idx + 1) % n
	}
	if cfg.dryRun {
		return sum, nil
	}
	return sum, state.Save()
}

// outcome is what process did with a track.
type outcome int

const (
	skipped  outcome = iota // rejected, unchanged, failed or a dry run
	sent                    // exported
	deferred                // needs exporting, but the run's limit is reached
)

// process validates one track and exports it when new or changed and
// canExport.
func process(ctx context.Context, cfg config, log *slog.Logger, state *State, x *Exporter, t Track, sum *summary, canExport bool) (outcome, error) {
	rec := state.Records[t.SourceID]
	reject := func(reason string) {
		sum.Rejected[reason]++
		if rec == nil || rec.RouteID == "" {
			state.Records[t.SourceID] = &Record{Rejected: reason, UpdatedAt: time.Now().UTC()}
		}
	}
	if reason := cfg.rules.Validate(&t); reason != "" {
		reject(reason)
		return skipped, nil
	}
	fp := t.Fingerprint()
	if rec != nil && rec.RouteID != "" && rec.Fingerprint == fp {
		sum.Unchanged++
		return skipped, nil
	}
	if rec == nil || rec.RouteID == "" {
		if dup := state.DuplicateOf(t); dup != "" {
			reject("same trail as another source's route")
			log.Debug("duplicate", "source_id", t.SourceID, "of", dup)
			return skipped, nil
		}
	}
	if !canExport {
		return deferred, nil
	}
	if cfg.dryRun {
		log.Info("would export", "source_id", t.SourceID, "name", t.Name, "km", int(t.LengthM()/1000))
		return skipped, nil
	}
	prev := ""
	if rec != nil {
		prev = rec.RouteID
	}
	routeID, err := x.Export(ctx, t, prev)
	if err != nil {
		if ctx.Err() != nil {
			return skipped, ctx.Err()
		}
		sum.Failed++
		log.Warn("export failed", "source_id", t.SourceID, "name", t.Name, "error", err)
		return skipped, nil
	}
	if prev != "" && prev == routeID {
		sum.Updated++
	} else {
		sum.New++
	}
	start, end := t.Ends()
	state.Records[t.SourceID] = &Record{RouteID: routeID, Fingerprint: fp, UpdatedAt: time.Now().UTC(),
		Start: &start, End: &end, LengthM: t.LengthM()}
	// Saved after every export so an interrupted run never repeats work.
	if err := state.Save(); err != nil {
		return sent, fmt.Errorf("save state: %w", err)
	}
	log.Info("exported", "source_id", t.SourceID, "name", t.Name, "route_id", routeID, "updated", prev != "")
	return sent, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
