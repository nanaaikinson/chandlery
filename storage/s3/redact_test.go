package s3_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/nanaaikinson/chandlery/storage/s3"
)

// A Config never shows its secret key, however it's printed: on its own or as
// a field of something larger, through fmt, slog's text and JSON handlers, or
// encoding/json.
func TestConfigNeverPrintsTheSecret(t *testing.T) {
	t.Parallel()
	const key = "very-secret-key"
	cfg := s3.Config{Endpoint: "r2.example:443", AccessKeyID: "id", SecretAccessKey: key}
	type app struct{ Storage s3.Config }

	var outputs []string
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		outputs = append(outputs, fmt.Sprintf(verb, cfg), fmt.Sprintf(verb, app{cfg}))
	}
	for _, h := range []func(*bytes.Buffer) slog.Handler{
		func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
		func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
	} {
		var buf bytes.Buffer
		slog.New(h(&buf)).Info("x", "config", cfg, "app", app{cfg})
		outputs = append(outputs, buf.String())
	}
	raw, err := json.Marshal(app{cfg})
	if err != nil {
		t.Fatal(err)
	}
	outputs = append(outputs, string(raw))

	for _, out := range outputs {
		if strings.Contains(out, key) {
			t.Errorf("printed the secret key: %s", out)
		}
		if !strings.Contains(out, "r2.example:443") {
			t.Errorf("lost the endpoint: %s", out)
		}
	}
}
