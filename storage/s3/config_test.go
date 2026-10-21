package s3_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/nanaaikinson/chandlery/storage/s3"
)

// ConfigFromEnv keeps New's local defaults when nothing is set. Not
// parallel: it sets environment variables.
func TestConfigFromEnvDefaults(t *testing.T) {
	for _, name := range []string{"S3_ENDPOINT", "S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY", "S3_REGION", "S3_USE_SSL"} {
		t.Setenv(name, "")
	}
	want := s3.Config{Endpoint: "127.0.0.1:9000", AccessKeyID: "minioadmin", SecretAccessKey: "minioadmin"}
	if got := s3.ConfigFromEnv(); got != want {
		t.Fatalf("ConfigFromEnv() = %+v, want %+v", got, want)
	}
	if _, err := s3.New("bucket"); err != nil {
		t.Fatalf("New with no environment: %v", err)
	}
}

// Not parallel: it sets environment variables.
func TestConfigFromEnvReadsEveryVariable(t *testing.T) {
	t.Setenv("S3_ENDPOINT", "r2.example:443")
	t.Setenv("S3_ACCESS_KEY_ID", "id")
	t.Setenv("S3_SECRET_ACCESS_KEY", "secret")
	t.Setenv("S3_REGION", "auto")
	t.Setenv("S3_USE_SSL", "true")
	want := s3.Config{Endpoint: "r2.example:443", AccessKeyID: "id", SecretAccessKey: "secret", Region: "auto", UseSSL: true}
	if got := s3.ConfigFromEnv(); got != want {
		t.Fatalf("ConfigFromEnv() = %+v, want %+v", got, want)
	}
}

// NewDisk has no defaults: a missing endpoint or credential is an error, not
// a quiet fallback to a local development server.
func TestNewDiskRefusesAnIncompleteConfig(t *testing.T) {
	t.Parallel()
	full := s3.Config{Endpoint: "r2.example:443", AccessKeyID: "id", SecretAccessKey: "secret", UseSSL: true}
	if _, err := s3.NewDisk(full, "bucket"); err != nil {
		t.Fatalf("NewDisk(complete) = %v", err)
	}
	for name, mutate := range map[string]func(*s3.Config){
		"no endpoint":   func(c *s3.Config) { c.Endpoint = "" },
		"no key id":     func(c *s3.Config) { c.AccessKeyID = "" },
		"no secret key": func(c *s3.Config) { c.SecretAccessKey = "" },
	} {
		cfg := full
		mutate(&cfg)
		if _, err := s3.NewDisk(cfg, "bucket"); err == nil {
			t.Errorf("%s: NewDisk succeeded", name)
		}
	}
}

// Logging a Config never shows the secret key.
func TestConfigLogValueRedactsTheSecret(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("s3", "config",
		s3.Config{Endpoint: "r2.example:443", AccessKeyID: "id", SecretAccessKey: "very-secret-key"})
	if strings.Contains(buf.String(), "very-secret-key") {
		t.Fatalf("log line quotes the secret key: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "r2.example:443") {
		t.Fatalf("log line lost the endpoint: %s", buf.String())
	}
}
