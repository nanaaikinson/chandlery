package fiber

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	z "github.com/Oudwins/zog"
	gofiber "github.com/gofiber/fiber/v3"

	"github.com/nanaaikinson/chandlery/validator"
)

type signupRequest struct {
	Email string
}

var signupSchema = z.Struct(z.Shape{
	"email": z.String().Required(),
})

func newApp(t *testing.T) *gofiber.App {
	t.Helper()
	app := gofiber.New()
	app.Post("/signup", ValidateRequest[signupRequest](signupSchema), func(c gofiber.Ctx) error {
		body := Body[signupRequest](c)
		return c.JSON(map[string]string{"email": body.Email})
	})
	return app
}

func doRequest(t *testing.T, app *gofiber.App, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/signup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	rec := httptest.NewRecorder()
	rec.Code = resp.StatusCode
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	rec.Body.Write(raw)
	return rec
}

func TestValidateRequest_MalformedBody(t *testing.T) {
	t.Parallel()
	rec := doRequest(t, newApp(t), `{not valid json`)

	if rec.Code != gofiber.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, gofiber.StatusBadRequest)
	}
}

func TestValidateRequest_ValidationFailure(t *testing.T) {
	t.Parallel()
	rec := doRequest(t, newApp(t), `{"email": ""}`)

	if rec.Code != gofiber.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", rec.Code, gofiber.StatusUnprocessableEntity)
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
	rec := doRequest(t, newApp(t), `{"email": "a@example.com"}`)

	if rec.Code != gofiber.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, gofiber.StatusOK)
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

	// app.Test runs the handler on its own goroutine, so a panic there can't
	// be caught by this test's own defer/recover — recover inside the
	// handler instead and surface the result through the response.
	app := gofiber.New()
	app.Get("/unvalidated", func(c gofiber.Ctx) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = c.SendString("panicked")
			}
		}()
		Body[signupRequest](c) // no ValidateRequest[signupRequest] wired for this route
		return c.SendString("no panic")
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/unvalidated", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	if string(raw) != "panicked" {
		t.Errorf("body = %q, want %q (Body[T] should panic when ValidateRequest[T] was never registered)", raw, "panicked")
	}
}
