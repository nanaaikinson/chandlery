// Command nethttp-example is a small, real (if minimal) task API
// demonstrating every chandlery package wired together behind the standard
// library: env for config, db/db-postgres for storage, cache (memory or
// redis, by CACHE_DRIVER) as a read-through cache in front of it,
// validator/nethttp for request validation, and respond/nethttp for the
// response envelope and centralized error mapping. See README.md for setup
// and a full endpoint list.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/nanaaikinson/chandlery/db"
	"github.com/nanaaikinson/chandlery/db/postgres"
	"github.com/nanaaikinson/chandlery/env"
	"github.com/nanaaikinson/chandlery/examples/internal/app"
	"github.com/nanaaikinson/chandlery/examples/internal/task"
	respondhttp "github.com/nanaaikinson/chandlery/respond/nethttp"
	validatornethttp "github.com/nanaaikinson/chandlery/validator/nethttp"
)

// requestTimeout bounds how long any single handler may run. Unlike Fiber
// (see examples/fiber/main.go), net/http's *http.Request already carries a
// real, cancelable per-request context — this just adds a deadline on top
// of it, via http.TimeoutHandler.
const requestTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("nethttp example exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// .env is optional (local dev convenience); real environment variables
	// always take precedence, since Load never overrides what's already set.
	_ = env.Load()
	app.ConfigureLogger()

	bunDB := postgres.New()
	defer bunDB.Close()

	setupCtx, cancelSetup := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelSetup()
	if err := postgres.Ping(setupCtx, bunDB); err != nil {
		return fmt.Errorf("connecting to postgres: %w", err)
	}
	if _, err := bunDB.ExecContext(setupCtx, task.TableSchema); err != nil {
		return fmt.Errorf("applying schema: %w", err)
	}

	cacheStore, closeCache, err := task.NewCacheStore(env.Get("CACHE_DRIVER", "memory"), "chandlery-nethttp-example:")
	if err != nil {
		return err
	}
	defer closeCache()

	h := &handlers{
		db:    bunDB,
		repo:  db.NewRepository[*task.Task](bunDB),
		cache: cacheStore,
		ttl:   time.Duration(env.GetInt("CACHE_TTL_SECONDS", 60)) * time.Second,
	}

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", respondhttp.Wrap(h.healthz))
	mux.Handle("POST /tasks", validatornethttp.ValidateRequest[task.CreateRequest](task.CreateSchema)(respondhttp.Wrap(h.createTask)))
	mux.Handle("GET /tasks", respondhttp.Wrap(h.listTasks))
	mux.Handle("GET /tasks/{id}", respondhttp.Wrap(h.getTask))
	mux.Handle("PATCH /tasks/{id}", validatornethttp.ValidateRequest[task.PatchRequest](task.PatchSchema)(respondhttp.Wrap(h.updateTask)))
	mux.Handle("DELETE /tasks/{id}", respondhttp.Wrap(h.deleteTask))

	handler := http.TimeoutHandler(withRequestID(mux), requestTimeout, `{"message":"request timed out","type":"internal_error"}`)
	return serve(handler, env.Get("PORT", "8080"))
}

// withRequestID assigns a request ID (reusing one an upstream proxy already
// set, if present), echoes it back on the response, and stores it on the
// request context for respondhttp's 5xx logging to pick up — net/http has
// no standard request-ID mechanism of its own, unlike Fiber's requestid
// middleware.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = ulid.Make().String()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(respondhttp.WithRequestID(r.Context(), id)))
	})
}

// serve runs handler until it's told to shut down (SIGINT/SIGTERM), then
// drains in-flight requests before returning.
func serve(handler http.Handler, port string) error {
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("listening", "port", port)

	select {
	case err := <-errCh:
		return fmt.Errorf("listen: %w", err)
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
