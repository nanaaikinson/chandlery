package fiber

import (
	"encoding/json"
	"errors"
	"io"
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
