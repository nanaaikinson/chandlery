package odm

import (
	"context"
	"fmt"
	"reflect"
)

// Creating runs before a model is inserted, and aborts the insert if it
// returns an error. Observers are the same lifecycle as the model's own
// hooks, moved outside the model type — for behavior that belongs to the
// application rather than to the document:
//
//	type UserObserver struct{ mailer *Mailer }
//
//	func (o UserObserver) Created(ctx context.Context, user *User) error {
//		return o.mailer.Welcome(ctx, user.Email)
//	}
//
//	odm.Observe[User](database, UserObserver{mailer: mailer})
//
// Implement only the events you need: each is its own interface, and an
// observer is asked about each one separately.
type Creating[T any] interface {
	Creating(ctx context.Context, model *T) error
}

// Created runs after a successful insert. The document is already written by
// then, so an error from it reaches the caller but undoes nothing.
type Created[T any] interface {
	Created(ctx context.Context, model *T) error
}

// Updating runs before Save writes a changed model, and aborts the write if
// it returns an error. Read what is about to change with odm.Changes or
// odm.Original.
type Updating[T any] interface {
	Updating(ctx context.Context, model *T) error
}

// Updated runs after Save has written a changed model. By then the model's
// snapshot has not yet been refreshed, so odm.Changes still reports what was
// just written.
type Updated[T any] interface {
	Updated(ctx context.Context, model *T) error
}

// Observe registers observers for model T on one database:
//
//	odm.Observe[User](database, UserObserver{})
//
// Registration is per DB, not per process: there is no package-global
// registry to be mutated from anywhere, and a second database — a test's,
// say — carries its own observers or none. Every collection built from that
// database sees them, whenever it was built.
//
// observers is an ...any rather than a typed parameter because Go has no way
// to say "implements at least one of these four": a single Observer[T]
// interface would force every observer to implement all four events, and one
// registration function per event would turn an observer that watches three
// of them into three calls. The cost is that a mismatch — almost always a
// signature typo, or an observer written for another model — can only be
// caught at registration, where it panics rather than going silently
// unregistered. Register at startup, alongside odm.New, and a typo fails the
// process before it serves anything.
func Observe[T any](db *DB, observers ...any) {
	if db == nil {
		panic("odm: Observe: db is nil")
	}

	for _, observer := range observers {
		if !observesAnything[T](observer) {
			panic(fmt.Sprintf(
				"odm: Observe[%[1]s]: %[2]T implements none of Creating[%[1]s], Created[%[1]s], Updating[%[1]s] or Updated[%[1]s] — check the method signatures, and that the model type matches",
				reflect.TypeFor[T](), observer,
			))
		}
	}

	db.observerMu.Lock()
	defer db.observerMu.Unlock()

	if db.observers == nil {
		db.observers = map[reflect.Type][]any{}
	}
	key := reflect.TypeFor[T]()
	db.observers[key] = append(db.observers[key], observers...)
}

func observesAnything[T any](observer any) bool {
	switch observer.(type) {
	case Creating[T], Created[T], Updating[T], Updated[T]:
		return true
	default:
		return false
	}
}

// observersFor returns the observers registered for a model type. The slice
// is the registry's own: registration appends a new one rather than writing
// into this, so a dispatch already in flight keeps reading a stable list.
func (db *DB) observersFor(key reflect.Type) []any {
	// A collection built without a database — as a unit test does to
	// exercise query compilation — has no observers rather than no answer.
	if db == nil {
		return nil
	}

	db.observerMu.RLock()
	defer db.observerMu.RUnlock()

	return db.observers[key]
}

// observerEvent names the four dispatch points.
type observerEvent string

const (
	eventCreating observerEvent = "Creating"
	eventCreated  observerEvent = "Created"
	eventUpdating observerEvent = "Updating"
	eventUpdated  observerEvent = "Updated"
)

// notify runs one event over every observer registered for T, in the order
// they were registered, stopping at the first error.
func notify[T any](ctx context.Context, db *DB, event observerEvent, model *T) error {
	observers := db.observersFor(reflect.TypeFor[T]())
	if len(observers) == 0 {
		return nil
	}

	for _, observer := range observers {
		var err error
		switch event {
		case eventCreating:
			if typed, ok := observer.(Creating[T]); ok {
				err = typed.Creating(ctx, model)
			}
		case eventCreated:
			if typed, ok := observer.(Created[T]); ok {
				err = typed.Created(ctx, model)
			}
		case eventUpdating:
			if typed, ok := observer.(Updating[T]); ok {
				err = typed.Updating(ctx, model)
			}
		case eventUpdated:
			if typed, ok := observer.(Updated[T]); ok {
				err = typed.Updated(ctx, model)
			}
		}
		if err != nil {
			return fmt.Errorf("odm: %s observer %T: %w", event, observer, err)
		}
	}
	return nil
}
