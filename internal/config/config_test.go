package config

import (
	"strings"
	"testing"
	"time"
)

func env(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func TestKeysRequiredOutsideDev(t *testing.T) {
	if _, err := LoadFrom(env()); err == nil || !strings.Contains(err.Error(), "JWT_PRIVATE_KEY_FILE") {
		t.Errorf("production without keys: err = %v", err)
	}
	c, err := LoadFrom(env("APP_ENV", "dev"))
	if err != nil || c.PrivateKeyFile != devPrivateKeyFile || c.PublicKeyFile != devPublicKeyFile {
		t.Errorf("dev: %+v, %v", c, err)
	}
	c, err = LoadFrom(env("JWT_PRIVATE_KEY_FILE", "a", "JWT_PUBLIC_KEY_FILE", "b"))
	if err != nil || c.PrivateKeyFile != "a" {
		t.Errorf("production with keys: %+v, %v", c, err)
	}
	if _, err := LoadFrom(env("APP_ENV", "prod")); err == nil {
		t.Error("unknown APP_ENV accepted")
	}
}

func TestDefaultsAndParsing(t *testing.T) {
	c, err := LoadFrom(env("APP_ENV", "dev", "METRICS_ADDR", "off", "CORS_ORIGINS", " a , ,b",
		"IMPORT_WORKERS", "5", "SHUTDOWN_TIMEOUT", "3s", "RATE_LIMIT_RPS", "0"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8080" || c.MetricsAddr != "" || c.PubSub != "nats" || strings.Join(c.CORSOrigins, "|") != "a|b" ||
		c.Jobs.Workers != 5 || c.ShutdownTimeout != 3*time.Second || c.RateLimits.PerSecond != 0 ||
		c.RateLimits.Burst != 40 {
		t.Errorf("unexpected config: %+v", c)
	}
	for _, kv := range [][2]string{{"IMPORT_WORKERS", "-1"}, {"SHUTDOWN_TIMEOUT", "0s"}, {"RATE_LIMIT_RPS", "x"}, {"PUBSUB", "kafka"}} {
		if _, err := LoadFrom(env("APP_ENV", "dev", kv[0], kv[1])); err == nil {
			t.Errorf("%s=%s accepted", kv[0], kv[1])
		}
	}
}
