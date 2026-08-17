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
	"strings"
	"syscall"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/nanaaikinson/chandlery/db"
	"github.com/nanaaikinson/chandlery/db/postgres"
	"github.com/nanaaikinson/chandlery/env"
	"github.com/nanaaikinson/chandlery/examples/internal/app"
	"github.com/nanaaikinson/chandlery/examples/internal/task"
	"github.com/nanaaikinson/chandlery/respond"
	respondhttp "github.com/nanaaikinson/chandlery/respond/nethttp"
	"github.com/nanaaikinson/chandlery/storage"
	"github.com/nanaaikinson/chandlery/storage/local"
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

	port := env.Get("PORT", "8080")
	storageDriver := env.Get("STORAGE_DRIVER", "local")
	// Only meaningful for the local driver, to build a signed URL that
	// resolves back to this app's own /storage route below — s3's signed
	// URLs point at the S3 endpoint directly and ignore this. TrimSuffix
	// matters: appURL+"/storage" without it would double a trailing slash
	// in APP_URL into a base URL like "http://host//storage", which the
	// registered route "/storage/{path...}" would never match.
	appURL := strings.TrimSuffix(env.Get("APP_URL", "http://localhost:"+port), "/")
	storageDisk, closeStorage, err := task.NewStorageDisk(setupCtx, storageDriver,
		env.Get("STORAGE_LOCAL_ROOT", "./storage-example-nethttp"), appURL+"/storage",
		env.Get("S3_BUCKET", "chandlery-example"), "chandlery-nethttp-example/")
	if err != nil {
		return err
	}
	defer closeStorage()

	h := &handlers{
		db:      bunDB,
		repo:    db.NewRepository[*task.Task](bunDB),
		cache:   cacheStore,
		ttl:     time.Duration(env.GetInt("CACHE_TTL_SECONDS", 60)) * time.Second,
		storage: storageDisk,
	}

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", respondhttp.Wrap(h.healthz))
	mux.Handle("POST /tasks", validatornethttp.ValidateRequest[task.CreateRequest](task.CreateSchema)(respondhttp.Wrap(h.createTask)))
	mux.Handle("GET /tasks", respondhttp.Wrap(h.listTasks))
	mux.Handle("GET /tasks/{id}", respondhttp.Wrap(h.getTask))
	mux.Handle("PATCH /tasks/{id}", validatornethttp.ValidateRequest[task.PatchRequest](task.PatchSchema)(respondhttp.Wrap(h.updateTask)))
	mux.Handle("DELETE /tasks/{id}", respondhttp.Wrap(h.deleteTask))

	mux.Handle("PUT /tasks/{id}/attachment", respondhttp.Wrap(h.uploadAttachment))
	mux.Handle("GET /tasks/{id}/attachment", respondhttp.Wrap(h.downloadAttachment))
	mux.Handle("DELETE /tasks/{id}/attachment", respondhttp.Wrap(h.deleteAttachment))
	mux.Handle("GET /tasks/{id}/attachment/url", respondhttp.Wrap(h.attachmentURL))

	// The local driver's signed URLs point back at this app (it's the only
	// thing that can serve them); s3's point straight at the S3 endpoint,
	// so there's nothing for this app to serve in that case.
	if localDisk, ok := storageDisk.(*local.Disk); ok {
		mux.Handle("GET /storage/{path...}", respondhttp.Wrap(serveSignedLocalFile(localDisk)))
	}

	handler := http.TimeoutHandler(withRequestID(mux), requestTimeout, `{"message":"request timed out","type":"internal_error"}`)
	return serve(handler, port)
}

// serveSignedLocalFile verifies the requesting URL's signature (minted by
// storage/local's TemporaryUrl) before serving the file it names — the
// "whatever handler does" storage/local's own doc comments point to.
func serveSignedLocalFile(d *local.Disk) respondhttp.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		// VerifySignedURL only ever looks at the path/query, so the
		// request-URI alone is enough — no need to reconstruct scheme+host.
		p, ok := d.VerifySignedURL(r.URL.RequestURI(), http.MethodGet)
		if !ok {
			return respond.NewStatusError(http.StatusForbidden, "invalid or expired signature")
		}

		data, err := d.Get(ctx, p)
		if err != nil {
			if errors.Is(err, storage.ErrFileNotFound) {
				return respond.NewStatusError(http.StatusNotFound, "file not found")
			}
			return respond.NewStatusError(http.StatusInternalServerError, "failed to read file", err)
		}

		contentType, err := d.ContentType(ctx, p)
		if err != nil {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		_, writeErr := w.Write(data)
		return writeErr
	}
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
