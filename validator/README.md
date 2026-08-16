# validator

Turns [zog](https://github.com/Oudwins/zog) validation issues into a
per-field error shape suitable for an API response:

```go
type ValidationError struct {
    Field   string   `json:"field"`
    Reasons []string `json:"reasons"`
}
```

`SanitizeIssues` groups zog's flat `ZogIssueList` into one `ValidationError`
per field, collecting every reason that field failed for — so three issues
against the same field become one entry with three reasons, not three
separate entries.

Two adapters build full request-validation middleware on top of it, each
pairing with the matching [`respond`](../respond) adapter:

- [`validator/nethttp`](nethttp) — for `net/http` and anything compatible
  with it.
- [`validator/fiber`](fiber) — for [Fiber v3](https://github.com/gofiber/fiber).

## Usage

Both adapters follow the same shape: `ValidateRequest[T](schema)` decodes and
validates the request body against a zog schema — a malformed body renders
as 400, a failed validation renders as 422 with `SanitizeIssues`' per-field
detail — then stores the validated `*T` for `Body[T]` to retrieve downstream.

### net/http

`validator/nethttp`'s package name is also `nethttp`, so alias it against
[`respond/nethttp`](../respond/nethttp) if you import both:

```go
import (
    respondhttp "github.com/nanaaikinson/chandlery/respond/nethttp"
    validatorhttp "github.com/nanaaikinson/chandlery/validator/nethttp"
)

mux := http.NewServeMux()
mux.Handle("/orders", validatorhttp.ValidateRequest[CreateOrder](createOrderSchema)(
    respondhttp.Wrap(func(w http.ResponseWriter, r *http.Request) error {
        order := validatorhttp.Body[CreateOrder](r)
        // order is already validated here.
        return nil
    }),
))
```

### Fiber

`validator/fiber`'s package name is also `fiber`, so alias it against both
Fiber itself and [`respond/fiber`](../respond/fiber):

```go
import (
    "github.com/gofiber/fiber/v3"
    respondfiber "github.com/nanaaikinson/chandlery/respond/fiber"
    validatorfiber "github.com/nanaaikinson/chandlery/validator/fiber"
)

app.Post("/orders", validatorfiber.ValidateRequest[CreateOrder](createOrderSchema), func(c fiber.Ctx) error {
    order := validatorfiber.Body[CreateOrder](c)
    // order is already validated here.
    return respondfiber.OK(c, "created")
})
```

`Body[T]` panics if called for a route that never ran the matching
`ValidateRequest[T]` — it's a wiring error, not a runtime condition a handler
should have to check for.
