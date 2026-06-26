package config

import (
	"strings"
	"testing"
)

// setRequired sets the minimum env for Load to succeed; callers override pieces.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("CAIRNMARK_POSTGRES_DSN", "postgres://localhost/db")
	t.Setenv("CAIRNMARK_S3_ENDPOINT", "localhost:9000")
	t.Setenv("CAIRNMARK_S3_ACCESS_KEY", "key")
	t.Setenv("CAIRNMARK_S3_SECRET_KEY", "secret")
	t.Setenv("CAIRNMARK_S3_BUCKET", "bucket")
}

func TestPublicEndpointDefaultsToEndpoint(t *testing.T) {
	setRequired(t)
	t.Setenv("CAIRNMARK_S3_USE_SSL", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Storage.PublicEndpoint != "localhost:9000" {
		t.Fatalf("PublicEndpoint: got %q want fallback to Endpoint", cfg.Storage.PublicEndpoint)
	}
	if !cfg.Storage.PublicUseSSL {
		t.Fatal("PublicUseSSL should default to UseSSL (true)")
	}
}

func TestPublicEndpointExplicitOverride(t *testing.T) {
	setRequired(t)
	t.Setenv("CAIRNMARK_S3_PUBLIC_ENDPOINT", "files.example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Storage.PublicEndpoint != "files.example.com" {
		t.Fatalf("PublicEndpoint: got %q", cfg.Storage.PublicEndpoint)
	}
	if cfg.Storage.PublicUseSSL {
		t.Fatal("PublicUseSSL should default to UseSSL (false here)")
	}
}

func TestEndpointSchemeRejected(t *testing.T) {
	setRequired(t)
	t.Setenv("CAIRNMARK_S3_ENDPOINT", "https://localhost:9000")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "without a scheme") {
		t.Fatalf("expected scheme rejection, got %v", err)
	}
}

func TestPublicEndpointWithPathRejected(t *testing.T) {
	setRequired(t)
	t.Setenv("CAIRNMARK_S3_PUBLIC_ENDPOINT", "files.example.com/objects")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "PUBLIC_ENDPOINT") {
		t.Fatalf("expected public endpoint rejection, got %v", err)
	}
}

func TestMissingRequiredReported(t *testing.T) {
	// No env set at all → all required vars reported.
	for _, k := range []string{
		"CAIRNMARK_POSTGRES_DSN", "CAIRNMARK_S3_ENDPOINT", "CAIRNMARK_S3_ACCESS_KEY",
		"CAIRNMARK_S3_SECRET_KEY", "CAIRNMARK_S3_BUCKET",
	} {
		t.Setenv(k, "")
	}
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "required env vars not set") {
		t.Fatalf("expected missing-vars error, got %v", err)
	}
}
