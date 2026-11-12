// Package config reads the API's settings from environment variables.
// README.md documents every variable.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/weeks6/travel-docs/internal/routes/imports"
	"github.com/weeks6/travel-docs/internal/server"
)

const (
	defaultDatabaseURL = "postgres://travel:travel@localhost:5432/travel_docs?sslmode=disable"
	// Committed development keys, used only with APP_ENV=dev.
	devPrivateKeyFile = "dev/keys/jwt.pem"
	devPublicKeyFile  = "dev/keys/jwt.pub.pem"
)

// Config is everything the API can be configured with.
type Config struct {
	DatabaseURL     string
	Addr            string
	MetricsAddr     string // "" = no metrics server
	PrivateKeyFile  string
	PublicKeyFile   string
	CORSOrigins     []string
	PubSub          string // "nats" or "local"
	NATSURL         string
	UploadSlots     int
	ShutdownTimeout time.Duration
	Jobs            imports.Config
	RateLimits      server.RateLimits
}

// Load reads the configuration from the process environment.
func Load() (Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom reads the configuration with getenv (os.Getenv in production).
func LoadFrom(getenv func(string) string) (Config, error) {
	p := envParser{getenv: getenv}
	c := Config{
		DatabaseURL:    p.str("DATABASE_URL", defaultDatabaseURL),
		Addr:           p.str("ADDR", ":8080"),
		MetricsAddr:    p.str("METRICS_ADDR", ":9090"),
		PrivateKeyFile: getenv("JWT_PRIVATE_KEY_FILE"),
		PublicKeyFile:  getenv("JWT_PUBLIC_KEY_FILE"),
		PubSub:         p.str("PUBSUB", "nats"),
		NATSURL:        p.str("NATS_URL", "nats://localhost:4222"),
	}
	// The repository's keys are public, so tokens signed with them can be
	// forged by anyone: use them only when explicitly in development.
	switch env := p.str("APP_ENV", "production"); env {
	case "dev":
		if c.PrivateKeyFile == "" && c.PublicKeyFile == "" {
			c.PrivateKeyFile, c.PublicKeyFile = devPrivateKeyFile, devPublicKeyFile
		}
	case "production":
	default:
		return c, fmt.Errorf("APP_ENV must be dev or production, got %q", env)
	}
	if c.PrivateKeyFile == "" || c.PublicKeyFile == "" {
		return c, errors.New("JWT_PRIVATE_KEY_FILE and JWT_PUBLIC_KEY_FILE are required " +
			"(or set APP_ENV=dev to use the development keys)")
	}
	if c.MetricsAddr == "off" {
		c.MetricsAddr = ""
	}
	if c.PubSub != "nats" && c.PubSub != "local" {
		return c, fmt.Errorf("PUBSUB must be nats or local, got %q", c.PubSub)
	}
	for _, o := range strings.Split(p.str("CORS_ORIGINS", "http://localhost:3000"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.CORSOrigins = append(c.CORSOrigins, o)
		}
	}

	c.UploadSlots = p.int("UPLOAD_SLOTS", imports.DefaultUploadSlots)
	c.ShutdownTimeout = p.duration("SHUTDOWN_TIMEOUT", 25*time.Second)
	c.Jobs = imports.Config{
		Workers:       p.int("IMPORT_WORKERS", 2),
		ShutdownGrace: p.duration("IMPORT_SHUTDOWN_GRACE", 15*time.Second),
		Retention:     p.duration("IMPORT_JOB_RETENTION", 7*24*time.Hour),
	}
	c.RateLimits = server.RateLimits{
		PerSecond:          p.float("RATE_LIMIT_RPS", 20),
		Burst:              p.int("RATE_LIMIT_BURST", 40),
		AuthPerMinute:      p.float("RATE_LIMIT_AUTH_PER_MINUTE", 20),
		AuthBurst:          p.int("RATE_LIMIT_AUTH_BURST", 10),
		LocationsPerSecond: p.float("RATE_LIMIT_LOCATIONS_RPS", 2),
		LocationsBurst:     p.int("RATE_LIMIT_LOCATIONS_BURST", 10),
	}
	return c, p.err
}

// envParser reads typed settings and remembers the first error.
type envParser struct {
	getenv func(string) string
	err    error
}

func (p *envParser) fail(key, v, want string) {
	if p.err == nil {
		p.err = fmt.Errorf("%s=%q must be %s", key, v, want)
	}
}

func (p *envParser) str(key, def string) string {
	if v := p.getenv(key); v != "" {
		return v
	}
	return def
}

func (p *envParser) int(key string, def int) int {
	v := p.getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		p.fail(key, v, "a non-negative integer")
	}
	return n
}

func (p *envParser) float(key string, def float64) float64 {
	v := p.getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || n < 0 {
		p.fail(key, v, "a non-negative number (0 disables the limit)")
	}
	return n
}

func (p *envParser) duration(key string, def time.Duration) time.Duration {
	v := p.getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		p.fail(key, v, "a positive duration such as 30s or 2h")
	}
	return d
}
