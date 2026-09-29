package fiber

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	gofiber "github.com/gofiber/fiber/v3"

	"github.com/nanaaikinson/chandlery/respond"
)

func decodeResponse(t *testing.T, resp *http.Response) respond.Response {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	var out respond.Response
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding response body %q: %v", raw, err)
	}
	return out
}

func TestOK(t *testing.T) {
	t.Parallel()

	app := gofiber.New()
	app.Get("/", func(c gofiber.Ctx) error {
		return OK(c, "done")
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != gofiber.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, gofiber.StatusOK)
	}
}

func TestErrorHandler(t *testing.T) {
	t.Parallel()

	newApp := func(handler gofiber.Handler) *gofiber.App {
		app := gofiber.New(gofiber.Config{ErrorHandler: ErrorHandler})
		app.Get("/", handler)
		return app
	}

	t.Run("StatusError renders its own status and message", func(t *testing.T) {
		t.Parallel()
		app := newApp(func(c gofiber.Ctx) error {
			return respond.NewStatusError(gofiber.StatusNotFound, "no such order")
		})

		resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		if resp.StatusCode != gofiber.StatusNotFound {
			t.Errorf("status = %d, want %d", resp.StatusCode, gofiber.StatusNotFound)
		}

		want := respond.Response{Message: "no such order", Type: respond.TypeNotFound}
		if body := decodeResponse(t, resp); body != want {
			t.Errorf("body = %+v, want %+v", body, want)
		}
	})

	t.Run("a native fiber.Error renders its own status and message", func(t *testing.T) {
		t.Parallel()
		app := newApp(func(c gofiber.Ctx) error {
			return gofiber.NewError(gofiber.StatusBadRequest, "bad payload")
		})

		resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		if resp.StatusCode != gofiber.StatusBadRequest {
			t.Errorf("status = %d, want %d", resp.StatusCode, gofiber.StatusBadRequest)
		}

		want := respond.Response{Message: "bad payload", Type: respond.TypeBadRequest}
		if body := decodeResponse(t, resp); body != want {
			t.Errorf("body = %+v, want %+v", body, want)
		}
	})

	t.Run("a 5xx fiber.Error hides its message behind the generic one", func(t *testing.T) {
		t.Parallel()
		app := newApp(func(c gofiber.Ctx) error {
			return gofiber.NewError(gofiber.StatusInternalServerError, "leaky detail")
		})

		resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		if resp.StatusCode != gofiber.StatusInternalServerError {
			t.Errorf("status = %d, want %d", resp.StatusCode, gofiber.StatusInternalServerError)
		}

		if body := decodeResponse(t, resp); body.Message != respond.InternalMessage {
			t.Errorf("Message = %q, want the generic internal message (server detail must not leak)", body.Message)
		}
	})

	t.Run("an unmapped error renders as a generic 500", func(t *testing.T) {
		t.Parallel()
		app := newApp(func(c gofiber.Ctx) error {
			return errors.New("something unexpected")
		})

		resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		if resp.StatusCode != gofiber.StatusInternalServerError {
			t.Errorf("status = %d, want %d", resp.StatusCode, gofiber.StatusInternalServerError)
		}

		want := respond.Response{Message: respond.InternalMessage, Type: respond.TypeInternal}
		if body := decodeResponse(t, resp); body != want {
			t.Errorf("body = %+v, want %+v", body, want)
		}
	})

	t.Run("a hand-built StatusError with an invalid Status falls back to 500", func(t *testing.T) {
		t.Parallel()
		app := newApp(func(c gofiber.Ctx) error {
			// Bypasses NewStatusError, so Status is left at its zero value —
			// must not be written as-is (c.Status(0) is invalid).
			return &respond.StatusError{Message: "oops"}
		})

		resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		if resp.StatusCode != gofiber.StatusInternalServerError {
			t.Errorf("status = %d, want %d", resp.StatusCode, gofiber.StatusInternalServerError)
		}

		want := respond.Response{Message: respond.InternalMessage, Type: respond.TypeInternal}
		if body := decodeResponse(t, resp); body != want {
			t.Errorf("body = %+v, want %+v", body, want)
		}
	})
}

type contextKey struct{}

// ctxHandler keeps each record and the context it was made with.
type ctxHandler struct {
	slog.Handler
	got     *[]context.Context
	records *[]slog.Record
}

func (ctxHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h ctxHandler) Handle(ctx context.Context, record slog.Record) error {
	*h.got = append(*h.got, ctx)
	*h.records = append(*h.records, record)
	return nil
}

// Not parallel: it swaps the default logger.
func TestInternalLogsWithRequestContext(t *testing.T) {
	var got []context.Context
	var records []slog.Record
	previous := slog.Default()
	slog.SetDefault(slog.New(ctxHandler{Handler: slog.DiscardHandler, got: &got, records: &records}))
	t.Cleanup(func() { slog.SetDefault(previous) })

	app := gofiber.New()
	app.Get("/", func(c gofiber.Ctx) error {
		c.SetContext(context.WithValue(c.Context(), contextKey{}, "request"))
		return Internal(c, errors.New("boom"))
	})
	if _, err := app.Test(httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if len(got) != 1 || got[0].Value(contextKey{}) != "request" {
		t.Fatalf("logged contexts = %v", got)
	}
	// Read after the request ends, when Fiber has reused its buffers.
	if _, err := app.Test(httptest.NewRequest("GET", "/other-path-to-overwrite", nil)); err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	records[0].Attrs(func(a slog.Attr) bool {
		if a.Key == "path" && a.Value.String() != "/" {
			t.Errorf("path = %q, want %q", a.Value.String(), "/")
		}
		return true
	})
}
