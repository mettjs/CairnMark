// Package config loads runtime configuration from the environment (12-factor).
// It is imported only by the composition root (cmd/server); no other package
// reads the environment directly.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the fully-resolved runtime configuration.
type Config struct {
	HTTPAddr        string        // listen address, e.g. ":8080"
	ShutdownTimeout time.Duration // graceful shutdown grace period
	PresignTTL      time.Duration // lifetime of presigned download URLs

	Postgres Postgres
	Storage  Storage
	GC       GC
}

// GC configures the background reconciliation sweep. A non-positive Interval
// disables it. GracePeriod is how old an unreferenced object must be before it
// is treated as an orphan — it must exceed the longest plausible upload so an
// in-flight write (object stored, row not yet committed) is never reclaimed.
// IdempotencyTTL is how long upload idempotency keys are retained before the
// sweep expires them (non-positive disables expiry).
type GC struct {
	Interval       time.Duration
	GracePeriod    time.Duration
	IdempotencyTTL time.Duration
}

// Postgres holds the metadata database connection settings.
type Postgres struct {
	DSN string // pgx connection string / URL
}

// Storage holds the S3-compatible backend settings.
//
// Endpoint is where the service performs all object I/O (often an internal
// network name like "minio:9000"). PublicEndpoint is the host baked into
// presigned URLs handed back to clients; it defaults to Endpoint and only
// differs when external callers reach the store by a different name than the
// service does. The host is part of the SigV4 signature, so presigned URLs are
// signed against PublicEndpoint directly — the host cannot be rewritten after.
type Storage struct {
	Endpoint       string // host:port for service-side I/O, no scheme
	PublicEndpoint string // host:port clients are redirected to; defaults to Endpoint
	Region         string
	AccessKey      string
	SecretKey      string
	Bucket         string
	UseSSL         bool // TLS for the service-side endpoint
	PublicUseSSL   bool // TLS scheme for presigned URLs; defaults to UseSSL
}

// Load reads configuration from the environment, applying defaults. It returns
// an error if a required value is missing or malformed. As of Phase 1 the
// Postgres and S3 settings are required — the server connects to both at boot.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:        getenv("CAIRNMARK_HTTP_ADDR", ":8080"),
		ShutdownTimeout: 10 * time.Second,
		PresignTTL:      15 * time.Minute,
		Postgres: Postgres{
			DSN: getenv("CAIRNMARK_POSTGRES_DSN", ""),
		},
		Storage: loadStorage(),
		GC: GC{
			Interval:       5 * time.Minute,
			GracePeriod:    time.Hour,
			IdempotencyTTL: 24 * time.Hour,
		},
	}

	if err := getduration("CAIRNMARK_SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout); err != nil {
		return Config{}, err
	}
	if err := getduration("CAIRNMARK_PRESIGN_TTL", &cfg.PresignTTL); err != nil {
		return Config{}, err
	}
	if err := getduration("CAIRNMARK_GC_INTERVAL", &cfg.GC.Interval); err != nil {
		return Config{}, err
	}
	if err := getduration("CAIRNMARK_GC_GRACE_PERIOD", &cfg.GC.GracePeriod); err != nil {
		return Config{}, err
	}
	if err := getduration("CAIRNMARK_IDEMPOTENCY_TTL", &cfg.GC.IdempotencyTTL); err != nil {
		return Config{}, err
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// loadStorage reads the S3 settings. PublicEndpoint/PublicUseSSL default to the
// service-side endpoint and TLS scheme when not explicitly set, so a single
// endpoint config keeps working unchanged.
func loadStorage() Storage {
	endpoint := getenv("CAIRNMARK_S3_ENDPOINT", "")
	useSSL := getbool("CAIRNMARK_S3_USE_SSL", false)
	return Storage{
		Endpoint:       endpoint,
		PublicEndpoint: getenv("CAIRNMARK_S3_PUBLIC_ENDPOINT", endpoint),
		Region:         getenv("CAIRNMARK_S3_REGION", "us-east-1"),
		AccessKey:      getenv("CAIRNMARK_S3_ACCESS_KEY", ""),
		SecretKey:      getenv("CAIRNMARK_S3_SECRET_KEY", ""),
		Bucket:         getenv("CAIRNMARK_S3_BUCKET", ""),
		UseSSL:         useSSL,
		PublicUseSSL:   getbool("CAIRNMARK_S3_PUBLIC_USE_SSL", useSSL),
	}
}

// validate enforces the values the running service cannot start without.
func (c Config) validate() error {
	var missing []string
	if c.Postgres.DSN == "" {
		missing = append(missing, "CAIRNMARK_POSTGRES_DSN")
	}
	if c.Storage.Endpoint == "" {
		missing = append(missing, "CAIRNMARK_S3_ENDPOINT")
	}
	if c.Storage.AccessKey == "" {
		missing = append(missing, "CAIRNMARK_S3_ACCESS_KEY")
	}
	if c.Storage.SecretKey == "" {
		missing = append(missing, "CAIRNMARK_S3_SECRET_KEY")
	}
	if c.Storage.Bucket == "" {
		missing = append(missing, "CAIRNMARK_S3_BUCKET")
	}
	if len(missing) > 0 {
		return fmt.Errorf("config: required env vars not set: %s", strings.Join(missing, ", "))
	}

	if err := validateEndpoint("CAIRNMARK_S3_ENDPOINT", c.Storage.Endpoint); err != nil {
		return err
	}
	if err := validateEndpoint("CAIRNMARK_S3_PUBLIC_ENDPOINT", c.Storage.PublicEndpoint); err != nil {
		return err
	}
	return nil
}

// validateEndpoint sanity-checks an S3 endpoint. minio-go expects a bare
// host:port, so a scheme, path, or whitespace is almost certainly a
// misconfiguration — reject it at boot rather than emit broken presigned URLs.
func validateEndpoint(name, v string) error {
	if v == "" {
		return nil
	}
	if strings.Contains(v, "://") {
		return fmt.Errorf("config: %s must be a bare host:port without a scheme, got %q", name, v)
	}
	if strings.ContainsAny(v, " /") {
		return fmt.Errorf("config: %s must be a bare host:port, got %q", name, v)
	}
	return nil
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

// getduration overrides *dst from the env var if set, returning a wrapped error
// on a malformed value. An unset var leaves the existing default in place.
func getduration(key string, dst *time.Duration) error {
	v, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	parsed, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("config: %s: %w", key, err)
	}
	*dst = parsed
	return nil
}

func getbool(key string, fallback bool) bool {
	switch os.Getenv(key) {
	case "1", "true", "TRUE", "True":
		return true
	case "0", "false", "FALSE", "False":
		return false
	default:
		return fallback
	}
}
