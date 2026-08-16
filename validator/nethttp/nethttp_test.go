package nethttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	z "github.com/Oudwins/zog"

	"github.com/nanaaikinson/chandlery/validator"
)

type signupRequest struct {
	Email string
}

var signupSchema = z.Struct(z.Shape{
	"email": z.String().Required(),
})

func newHandler() http.Handler {
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := Body[signupRequest](r)
		_ = json.NewEncoder(w).Encode(map[string]string{"email": body.Email})
	})
	return ValidateRequest[signupRequest](signupSchema)(final)
}

func doRequest(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, req)
	return rec
}

func TestValidateRequest_MalformedBody(t *testing.T) {
	t.Parallel()
	rec := doRequest(t, `{not valid json`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestValidateRequest_ValidationFailure(t *testing.T) {
	t.Parallel()
	rec := doRequest(t, `{"email": ""}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	var body struct {
		Errors []validator.ValidationError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response body %q: %v", rec.Body.String(), err)
	}
	if len(body.Errors) == 0 {
		t.Fatal("expected per-field validation errors, got none")
	}
}

func TestValidateRequest_Success(t *testing.T) {
	t.Parallel()
	rec := doRequest(t, `{"email": "a@example.com"}`)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response body %q: %v", rec.Body.String(), err)
	}
	if body["email"] != "a@example.com" {
		t.Errorf("email = %q, want %q (Body[T] should return the validated request)", body["email"], "a@example.com")
	}
}

func TestBody_PanicsWithoutValidateRequest(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("Body[T] did not panic when ValidateRequest[T] was never registered on this route")
		}
	}()

	req := httptest.NewRequest(http.MethodGet, "/unvalidated", nil)
	Body[signupRequest](req) // no ValidateRequest[signupRequest] middleware ran
}
