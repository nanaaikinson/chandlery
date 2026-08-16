// Command fiber-example is a small, real (if minimal) task API demonstrating
// every chandlery package wired together behind Fiber v3: env for config,
// db/db-postgres for storage, cache (memory or redis, by CACHE_DRIVER) as a
// read-through cache in front of it, validator/fiber for request validation,
// and respond/fiber for the response envelope and centralized error mapping.
// See README.md for setup and a full endpoint list.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	gofiber "github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/gofiber/fiber/v3/middleware/timeout"

	"github.com/nanaaikinson/chandlery/db"
	"github.com/nanaaikinson/chandlery/db/postgres"
	"github.com/nanaaikinson/chandlery/env"
	"github.com/nanaaikinson/chandlery/examples/internal/app"
	"github.com/nanaaikinson/chandlery/examples/internal/task"
	respondfiber "github.com/nanaaikinson/chandlery/respond/fiber"
	validatorfiber "github.com/nanaaikinson/chandlery/validator/fiber"
)

// requestTimeout bounds how long any single handler may run. It also gives
// c.Context() something real to return: Fiber's Ctx.Context() is an inert,
// never-cancelled context.Background() unless something calls SetContext
// first (fasthttp, unlike net/http, has no per-request context of its own)
// — wrapping every route in middleware/timeout is what wires a real,
// deadline-bound, cancelable context in.
const requestTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("fiber example exited", "error", err)
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

	cacheStore, closeCache, err := task.NewCacheStore(env.Get("CACHE_DRIVER", "memory"), "chandlery-fiber-example:")
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

	fiberApp := gofiber.New(gofiber.Config{ErrorHandler: respondfiber.ErrorHandler})
	// Wires the request ID respondfiber.ErrorHandler's 5xx logging reads via
	// requestid.FromContext.
	fiberApp.Use(requestid.New())

	withTimeout := func(handler gofiber.Handler) gofiber.Handler {
		return timeout.New(handler, timeout.Config{Timeout: requestTimeout})
	}

	fiberApp.Get("/healthz", withTimeout(h.healthz))
	fiberApp.Post("/tasks", validatorfiber.ValidateRequest[task.CreateRequest](task.CreateSchema), withTimeout(h.createTask))
	fiberApp.Get("/tasks", withTimeout(h.listTasks))
	fiberApp.Get("/tasks/:id", withTimeout(h.getTask))
	fiberApp.Patch("/tasks/:id", validatorfiber.ValidateRequest[task.PatchRequest](task.PatchSchema), withTimeout(h.updateTask))
	fiberApp.Delete("/tasks/:id", withTimeout(h.deleteTask))

	return serve(fiberApp, env.Get("PORT", "8080"))
}

// serve runs fiberApp until it's told to shut down (SIGINT/SIGTERM), then
// drains in-flight requests before returning.
func serve(fiberApp *gofiber.App, port string) error {
	errCh := make(chan error, 1)
	go func() {
		if err := fiberApp.Listen(":" + port); err != nil {
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

	if err := fiberApp.ShutdownWithContext(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
