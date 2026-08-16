package nethttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nanaaikinson/chandlery/respond"
)

func decode(t *testing.T, rec *httptest.ResponseRecorder) respond.Response {
	t.Helper()
	var body respond.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response body %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestOK(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	OK(rec, "done")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := decode(t, rec), respond.OK("done"); got != want {
		t.Errorf("body = %+v, want %+v", got, want)
	}
}

func TestFail(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	Fail(rec, http.StatusConflict, respond.TypeConflict, "already exists")

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	want := respond.Response{Message: "already exists", Type: respond.TypeConflict}
	if got := decode(t, rec); got != want {
		t.Errorf("body = %+v, want %+v", got, want)
	}
}

func TestInternal(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/orders", nil)

	Internal(rec, req, errors.New("db exploded"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	want := respond.Response{Message: respond.InternalMessage, Type: respond.TypeInternal}
	if got := decode(t, rec); got != want {
		t.Errorf("body = %+v, want %+v (internal detail must never leak to the client)", got, want)
	}
}

func TestWrap(t *testing.T) {
	t.Parallel()

	t.Run("no error passes through untouched", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)

		Wrap(func(w http.ResponseWriter, r *http.Request) error {
			OK(w, "handled")
			return nil
		})(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	})

	t.Run("StatusError renders its own status and message", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)

		Wrap(func(w http.ResponseWriter, r *http.Request) error {
			return respond.NewStatusError(http.StatusNotFound, "no such order")
		})(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
		want := respond.Response{Message: "no such order", Type: respond.TypeNotFound}
		if got := decode(t, rec); got != want {
			t.Errorf("body = %+v, want %+v", got, want)
		}
	})

	t.Run("a 5xx StatusError hides its message behind the generic one", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)

		Wrap(func(w http.ResponseWriter, r *http.Request) error {
			return respond.NewStatusError(http.StatusInternalServerError, "leaky detail: table users missing column x")
		})(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
		got := decode(t, rec)
		if got.Message != respond.InternalMessage {
			t.Errorf("Message = %q, want the generic internal message (server detail must not leak)", got.Message)
		}
	})

	t.Run("an unmapped error renders as a generic 500", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)

		Wrap(func(w http.ResponseWriter, r *http.Request) error {
			return errors.New("something unexpected")
		})(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
		want := respond.Response{Message: respond.InternalMessage, Type: respond.TypeInternal}
		if got := decode(t, rec); got != want {
			t.Errorf("body = %+v, want %+v", got, want)
		}
	})

	t.Run("a hand-built StatusError with an invalid Status falls back to 500", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)

		Wrap(func(w http.ResponseWriter, r *http.Request) error {
			// Bypasses NewStatusError, so Status is left at its zero value —
			// must not be written as-is (w.WriteHeader(0) is invalid).
			return &respond.StatusError{Message: "oops"}
		})(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
		want := respond.Response{Message: respond.InternalMessage, Type: respond.TypeInternal}
		if got := decode(t, rec); got != want {
			t.Errorf("body = %+v, want %+v", got, want)
		}
	})
}

func TestRequestIDFromContext(t *testing.T) {
	t.Parallel()

	if got := RequestIDFromContext(t.Context()); got != "" {
		t.Errorf("RequestIDFromContext(no id set) = %q, want empty", got)
	}

	ctx := WithRequestID(t.Context(), "req-123")
	if got := RequestIDFromContext(ctx); got != "req-123" {
		t.Errorf("RequestIDFromContext() = %q, want %q", got, "req-123")
	}
}
