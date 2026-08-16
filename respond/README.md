# respond

Standard JSON response envelope, framework-agnostic at its core:

```json
{"message": "...", "type": "not_found", "errors": [...]}
```

`Errors` and `Type` are omitted when empty, so a plain success is just
`{"message": "..."}`.

The root package (`respond`) only builds response bodies and classifies
errors — it never writes to a wire. Two adapters do the actual writing and
logging:

- [`respond/nethttp`](nethttp) — for `net/http` and anything compatible with
  it (chi, gorilla/mux, ...).
- [`respond/fiber`](fiber) — for [Fiber v3](https://github.com/gofiber/fiber).

## Usage

Return a `*respond.StatusError` from a handler for an expected client error;
any other error is treated as an unexpected 500 and logged, never leaked to
the client.

### net/http

```go
mux.HandleFunc("/orders/{id}", nethttp.Wrap(func(w http.ResponseWriter, r *http.Request) error {
    order, err := lookupOrder(r.PathValue("id"))
    if errors.Is(err, errNotFound) {
        return respond.NewStatusError(http.StatusNotFound, "no such order")
    }
    if err != nil {
        return err // logged, rendered as a generic 500
    }
    nethttp.Data(w, http.StatusOK, order)
    return nil
}))
```

### Fiber

`respond/fiber`'s package name is also `fiber`, so alias it against Fiber
itself on import:

```go
import (
    "github.com/gofiber/fiber/v3"
    respondfiber "github.com/nanaaikinson/chandlery/respond/fiber"
)

app := fiber.New(fiber.Config{ErrorHandler: respondfiber.ErrorHandler})

app.Get("/orders/:id", func(c fiber.Ctx) error {
    order, err := lookupOrder(c.Params("id"))
    if errors.Is(err, errNotFound) {
        return respond.NewStatusError(fiber.StatusNotFound, "no such order")
    }
    if err != nil {
        return err // logged, rendered as a generic 500
    }
    return respondfiber.Data(c, fiber.StatusOK, order)
})
```
