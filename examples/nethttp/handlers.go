package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/nanaaikinson/chandlery/cache"
	"github.com/nanaaikinson/chandlery/db"
	"github.com/nanaaikinson/chandlery/db/postgres"
	"github.com/nanaaikinson/chandlery/examples/internal/app"
	"github.com/nanaaikinson/chandlery/examples/internal/task"
	"github.com/nanaaikinson/chandlery/respond"
	respondhttp "github.com/nanaaikinson/chandlery/respond/nethttp"
	validatornethttp "github.com/nanaaikinson/chandlery/validator/nethttp"
)

// handlers holds every dependency the route handlers need. Building it once
// in main and hanging methods off it (rather than closing over globals)
// keeps each handler a plain, testable function of its own state.
type handlers struct {
	db    *bun.DB
	repo  db.Repository[*task.Task]
	cache cache.Store
	ttl   time.Duration
}

func (h *handlers) healthz(w http.ResponseWriter, r *http.Request) error {
	if err := postgres.Ping(r.Context(), h.db); err != nil {
		return respond.NewStatusError(http.StatusServiceUnavailable, "database unreachable", err)
	}
	return respondhttp.OK(w, "ok")
}

func (h *handlers) createTask(w http.ResponseWriter, r *http.Request) error {
	body := validatornethttp.Body[task.CreateRequest](r)

	t := &task.Task{Title: body.Title, Done: body.Done}
	if err := h.repo.Create(r.Context(), t); err != nil {
		return respond.NewStatusError(http.StatusInternalServerError, "failed to create task", err)
	}

	return respondhttp.Data(w, http.StatusCreated, t)
}

func (h *handlers) listTasks(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	limit := app.ClampInt(r.URL.Query().Get("limit"), 1, 100, 20)
	offset := app.ClampInt(r.URL.Query().Get("offset"), 0, 1<<31-1, 0)

	// GetMany's page and Count's total are independent reads; running them
	// concurrently instead of back-to-back means listTasks pays roughly the
	// slower of the two round trips, not their sum.
	var (
		items    []*task.Task
		count    int
		getErr   error
		countErr error
		wg       sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		items, getErr = h.repo.GetMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Order("created_at DESC").Limit(limit).Offset(offset)
		})
	}()
	go func() {
		defer wg.Done()
		// Its own query, so it reports the real total rather than "did a
		// full page come back."
		count, countErr = h.repo.Count(ctx, func(q *bun.SelectQuery) *bun.SelectQuery { return q })
	}()
	wg.Wait()

	if getErr != nil {
		return respond.NewStatusError(http.StatusInternalServerError, "failed to list tasks", getErr)
	}
	if countErr != nil {
		return respond.NewStatusError(http.StatusInternalServerError, "failed to count tasks", countErr)
	}

	return respondhttp.Data(w, http.StatusOK, task.List{Items: items, Count: count, Limit: limit, Offset: offset})
}

func (h *handlers) getTask(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	cacheKey := task.CacheKey(id)

	// CACHE_TTL_SECONDS <= 0 disables caching for this handler entirely,
	// rather than reaching cache.Store.Put's own "ttl <= 0 means no expiry"
	// — an operator setting it to 0 expecting "don't cache this" would
	// otherwise get entries that are cached forever instead.
	cachingEnabled := h.ttl > 0

	// Cache-aside read: a hit skips Postgres entirely. A corrupt or
	// unreadable cache entry falls through to the database rather than
	// failing the request.
	if cachingEnabled {
		if cached, ok, err := h.cache.Get(ctx, cacheKey); err == nil && ok {
			var t task.Task
			if err := json.Unmarshal(cached, &t); err == nil {
				return respondhttp.Data(w, http.StatusOK, &t)
			}
		}
	}

	t, err := h.repo.GetOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("id = ?", id)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return respond.NewStatusError(http.StatusNotFound, "task not found")
		}
		return respond.NewStatusError(http.StatusInternalServerError, "failed to load task", err)
	}

	if cachingEnabled {
		h.fillCache(cacheKey, t)
	}

	return respondhttp.Data(w, http.StatusOK, t)
}

// fillCache populates the cache in the background: it's best-effort (the
// read it follows already succeeded against the database) and its result
// only matters to a later request, so there's no reason to make this one
// wait on a Redis round trip it's about to discard the outcome of.
func (h *handlers) fillCache(cacheKey string, t *task.Task) {
	body, err := json.Marshal(t)
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.cache.Put(ctx, cacheKey, body, h.ttl); err != nil {
			slog.Warn("cache fill failed", "key", cacheKey, "error", err)
		}
	}()
}

func (h *handlers) updateTask(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	body := validatornethttp.Body[task.PatchRequest](r)

	if body.Title == nil && body.Done == nil {
		return respond.NewStatusError(http.StatusBadRequest, "at least one of title or done must be provided")
	}

	// ConditionalUpdate reports whether a row actually matched, in one round
	// trip — a plain Update() can't tell an update-of-nothing apart from a
	// real one, which is what a 404 here depends on.
	applied, err := db.ConditionalUpdate[task.Task](ctx, h.repo.DB(), func(q *bun.UpdateQuery) *bun.UpdateQuery {
		q = q.Where("id = ?", id).Set("updated_at = ?", time.Now())
		if body.Title != nil {
			q = q.Set("title = ?", *body.Title)
		}
		if body.Done != nil {
			q = q.Set("done = ?", *body.Done)
		}
		return q
	})
	if err != nil {
		return respond.NewStatusError(http.StatusInternalServerError, "failed to update task", err)
	}
	if !applied {
		return respond.NewStatusError(http.StatusNotFound, "task not found")
	}

	h.invalidateCache(ctx, task.CacheKey(id))

	return respondhttp.OK(w, "task updated")
}

func (h *handlers) deleteTask(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")

	// Idempotent: deleting an id that's already gone (or never existed) is
	// still success, per DELETE's usual semantics — so this skips the
	// separate existence check updateTask needs (it has no equivalent of
	// ConditionalUpdate's "did anything match" signal to build one on
	// without an extra round trip, and doesn't need one here).
	victim := &task.Task{}
	victim.ID = id
	if err := h.repo.Delete(ctx, victim); err != nil {
		return respond.NewStatusError(http.StatusInternalServerError, "failed to delete task", err)
	}

	h.invalidateCache(ctx, task.CacheKey(id))

	w.WriteHeader(http.StatusNoContent)
	return nil
}

// invalidateCache forgets key, logging rather than failing the request on
// error: the write it follows already committed, so reporting a 500 for it
// would tell the client a successful mutation had failed. The tradeoff is a
// cache entry that can stay stale until its ttl expires — accepted here
// rather than closing that gap with a distributed lock or retry.
func (h *handlers) invalidateCache(ctx context.Context, key string) {
	if err := h.cache.Forget(ctx, key); err != nil {
		slog.Warn("cache invalidation failed", "key", key, "error", err)
	}
}
