package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	gofiber "github.com/gofiber/fiber/v3"
	"github.com/uptrace/bun"

	"github.com/nanaaikinson/chandlery/cache"
	"github.com/nanaaikinson/chandlery/db"
	"github.com/nanaaikinson/chandlery/db/postgres"
	"github.com/nanaaikinson/chandlery/examples/internal/app"
	"github.com/nanaaikinson/chandlery/examples/internal/task"
	"github.com/nanaaikinson/chandlery/respond"
	respondfiber "github.com/nanaaikinson/chandlery/respond/fiber"
	"github.com/nanaaikinson/chandlery/storage"
	validatorfiber "github.com/nanaaikinson/chandlery/validator/fiber"
)

// handlers holds every dependency the route handlers need. Building it once
// in main and hanging methods off it (rather than closing over globals)
// keeps each handler a plain, testable function of its own state.
type handlers struct {
	db      *bun.DB
	repo    db.Repository[*task.Task]
	cache   cache.Store
	ttl     time.Duration
	storage storage.Disk
}

func (h *handlers) healthz(c gofiber.Ctx) error {
	if err := postgres.Ping(c.Context(), h.db); err != nil {
		return respond.NewStatusError(gofiber.StatusServiceUnavailable, "database unreachable", err)
	}
	return respondfiber.OK(c, "ok")
}

func (h *handlers) createTask(c gofiber.Ctx) error {
	body := validatorfiber.Body[task.CreateRequest](c)

	t := &task.Task{Title: body.Title, Done: body.Done}
	if err := h.repo.Create(c.Context(), t); err != nil {
		return respond.NewStatusError(gofiber.StatusInternalServerError, "failed to create task", err)
	}

	return respondfiber.Data(c, gofiber.StatusCreated, t)
}

func (h *handlers) listTasks(c gofiber.Ctx) error {
	ctx := c.Context()
	limit := app.ClampInt(c.Query("limit", "20"), 1, 100, 20)
	offset := app.ClampInt(c.Query("offset", "0"), 0, 1<<31-1, 0)

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
		return respond.NewStatusError(gofiber.StatusInternalServerError, "failed to list tasks", getErr)
	}
	if countErr != nil {
		return respond.NewStatusError(gofiber.StatusInternalServerError, "failed to count tasks", countErr)
	}

	return respondfiber.Data(c, gofiber.StatusOK, task.List{Items: items, Count: count, Limit: limit, Offset: offset})
}

func (h *handlers) getTask(c gofiber.Ctx) error {
	ctx := c.Context()
	id := c.Params("id")
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
				return respondfiber.Data(c, gofiber.StatusOK, &t)
			}
		}
	}

	t, err := h.repo.GetOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("id = ?", id)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return respond.NewStatusError(gofiber.StatusNotFound, "task not found")
		}
		return respond.NewStatusError(gofiber.StatusInternalServerError, "failed to load task", err)
	}

	if cachingEnabled {
		h.fillCache(cacheKey, t)
	}

	return respondfiber.Data(c, gofiber.StatusOK, t)
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

func (h *handlers) updateTask(c gofiber.Ctx) error {
	ctx := c.Context()
	id := c.Params("id")
	body := validatorfiber.Body[task.PatchRequest](c)

	if body.Title == nil && body.Done == nil {
		return respond.NewStatusError(gofiber.StatusBadRequest, "at least one of title or done must be provided")
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
		return respond.NewStatusError(gofiber.StatusInternalServerError, "failed to update task", err)
	}
	if !applied {
		return respond.NewStatusError(gofiber.StatusNotFound, "task not found")
	}

	h.invalidateCache(ctx, task.CacheKey(id))

	return respondfiber.OK(c, "task updated")
}

func (h *handlers) deleteTask(c gofiber.Ctx) error {
	ctx := c.Context()
	id := c.Params("id")

	// Idempotent: deleting an id that's already gone (or never existed) is
	// still success, per DELETE's usual semantics — so this skips the
	// separate existence check updateTask needs (it has no equivalent of
	// ConditionalUpdate's "did anything match" signal to build one on
	// without an extra round trip, and doesn't need one here).
	victim := &task.Task{}
	victim.ID = id
	if err := h.repo.Delete(ctx, victim); err != nil {
		return respond.NewStatusError(gofiber.StatusInternalServerError, "failed to delete task", err)
	}

	h.invalidateCache(ctx, task.CacheKey(id))

	// Best-effort, logged rather than failing the request: the task row is
	// already gone, and leaving an orphaned attachment behind is a smaller
	// problem than reporting a successful delete as failed.
	if err := h.storage.Delete(ctx, task.AttachmentKey(id)); err != nil {
		slog.Warn("attachment cleanup failed", "id", id, "error", err)
	}

	return c.SendStatus(gofiber.StatusNoContent)
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

// attachmentTTL is how long an attachmentURL response's signed link stays
// valid.
const attachmentTTL = 15 * time.Minute

func (h *handlers) uploadAttachment(c gofiber.Ctx) error {
	ctx := c.Context()
	id := c.Params("id")

	exists, err := h.repo.Exists(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("id = ?", id)
	})
	if err != nil {
		return respond.NewStatusError(gofiber.StatusInternalServerError, "failed to look up task", err)
	}
	if !exists {
		return respond.NewStatusError(gofiber.StatusNotFound, "task not found")
	}

	if err := h.storage.Put(ctx, task.AttachmentKey(id), bytes.NewReader(c.Body())); err != nil {
		return respond.NewStatusError(gofiber.StatusInternalServerError, "failed to store attachment", err)
	}

	return respondfiber.OK(c, "attachment uploaded")
}

func (h *handlers) downloadAttachment(c gofiber.Ctx) error {
	ctx := c.Context()
	key := task.AttachmentKey(c.Params("id"))

	data, err := h.storage.Get(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrFileNotFound) {
			return respond.NewStatusError(gofiber.StatusNotFound, "no attachment for this task")
		}
		return respond.NewStatusError(gofiber.StatusInternalServerError, "failed to load attachment", err)
	}

	// Best-effort: an undetectable type still serves the bytes rather than
	// failing a download that already succeeded.
	contentType, err := h.storage.ContentType(ctx, key)
	if err != nil {
		contentType = "application/octet-stream"
	}
	c.Set(gofiber.HeaderContentType, contentType)
	return c.Send(data)
}

func (h *handlers) deleteAttachment(c gofiber.Ctx) error {
	if err := h.storage.Delete(c.Context(), task.AttachmentKey(c.Params("id"))); err != nil {
		return respond.NewStatusError(gofiber.StatusInternalServerError, "failed to delete attachment", err)
	}
	return c.SendStatus(gofiber.StatusNoContent)
}

// attachmentURL hands back a time-limited link instead of streaming the
// file through this handler: a real client downloads straight from S3 (or,
// on the local driver, from this app's own /storage route — see main.go)
// rather than doubling this process's bandwidth as a proxy.
func (h *handlers) attachmentURL(c gofiber.Ctx) error {
	ctx := c.Context()
	key := task.AttachmentKey(c.Params("id"))

	exists, err := h.storage.Exists(ctx, key)
	if err != nil {
		return respond.NewStatusError(gofiber.StatusInternalServerError, "failed to look up attachment", err)
	}
	if !exists {
		return respond.NewStatusError(gofiber.StatusNotFound, "no attachment for this task")
	}

	url, err := h.storage.TemporaryUrl(ctx, key, attachmentTTL)
	if err != nil {
		return respond.NewStatusError(gofiber.StatusInternalServerError, "failed to sign url", err)
	}
	return respondfiber.Data(c, gofiber.StatusOK, task.AttachmentURL{URL: url, ExpiresInSeconds: int(attachmentTTL.Seconds())})
}
