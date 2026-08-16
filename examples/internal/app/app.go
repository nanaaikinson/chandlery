// Package app holds the handful of things that aren't specific to either
// example's domain (task.go) or its HTTP framework (each example's own
// main.go/handlers.go): logging setup and a small query-param helper. Kept
// separate from the task package so that one stays purely about the
// example's data model.
package app

import (
	"log/slog"
	"os"
	"strconv"

	"github.com/nanaaikinson/chandlery/env"
)

// ConfigureLogger picks a human-readable text handler locally and
// structured JSON everywhere else (staging/production), so log shipping
// doesn't need to special-case the local dev format.
func ConfigureLogger() {
	if env.IsLocal() {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
		return
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
}

// ClampInt parses s as an int, falling back to def if it's missing or
// invalid, then clamps the result to [min, max].
func ClampInt(s string, min, max, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}
