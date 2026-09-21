package odm

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// recorder implements every event, recording the order they ran in.
type recorder struct {
	name  string
	calls *[]string
	fail  error
}

func (r recorder) Creating(_ context.Context, _ *tracked) error {
	*r.calls = append(*r.calls, r.name+":creating")
	return r.fail
}

func (r recorder) Created(_ context.Context, _ *tracked) error {
	*r.calls = append(*r.calls, r.name+":created")
	return r.fail
}

func (r recorder) Updating(_ context.Context, _ *tracked) error {
	*r.calls = append(*r.calls, r.name+":updating")
	return r.fail
}

func (r recorder) Updated(_ context.Context, _ *tracked) error {
	*r.calls = append(*r.calls, r.name+":updated")
	return r.fail
}

// creationOnly implements one event, which is all an observer has to.
type creationOnly struct{ calls *[]string }

func (o creationOnly) Created(_ context.Context, _ *tracked) error {
	*o.calls = append(*o.calls, "creationOnly:created")
	return nil
}

func TestObserve(t *testing.T) {
	t.Parallel()

	t.Run("dispatches in registration order", func(t *testing.T) {
		t.Parallel()

		var calls []string
		db := &DB{}
		Observe[tracked](db, recorder{name: "first", calls: &calls}, recorder{name: "second", calls: &calls})

		if err := notify(context.Background(), db, eventCreated, &tracked{}); err != nil {
			t.Fatalf("notify() error = %v", err)
		}
		want := []string{"first:created", "second:created"}
		if !reflect.DeepEqual(calls, want) {
			t.Errorf("calls = %v, want %v", calls, want)
		}
	})

	t.Run("skips an observer that does not implement the event", func(t *testing.T) {
		t.Parallel()

		var calls []string
		db := &DB{}
		Observe[tracked](db, creationOnly{calls: &calls})

		if err := notify(context.Background(), db, eventUpdating, &tracked{}); err != nil {
			t.Fatalf("notify() error = %v", err)
		}
		if len(calls) != 0 {
			t.Errorf("calls = %v, want none", calls)
		}

		if err := notify(context.Background(), db, eventCreated, &tracked{}); err != nil {
			t.Fatalf("notify() error = %v", err)
		}
		if want := []string{"creationOnly:created"}; !reflect.DeepEqual(calls, want) {
			t.Errorf("calls = %v, want %v", calls, want)
		}
	})

	t.Run("stops at the first failure", func(t *testing.T) {
		t.Parallel()

		var calls []string
		sentinel := errors.New("no")
		db := &DB{}
		Observe[tracked](db,
			recorder{name: "first", calls: &calls, fail: sentinel},
			recorder{name: "second", calls: &calls},
		)

		err := notify(context.Background(), db, eventCreating, &tracked{})
		if !errors.Is(err, sentinel) {
			t.Fatalf("notify() error = %v, want the observer's own error", err)
		}
		if want := []string{"first:creating"}; !reflect.DeepEqual(calls, want) {
			t.Errorf("calls = %v, want the second observer not to have run", calls)
		}
	})

	t.Run("keeps each model type's observers apart", func(t *testing.T) {
		t.Parallel()

		var calls []string
		db := &DB{}
		Observe[tracked](db, recorder{name: "tracked", calls: &calls})

		if err := notify(context.Background(), db, eventCreated, &User{}); err != nil {
			t.Fatalf("notify() error = %v", err)
		}
		if len(calls) != 0 {
			t.Errorf("calls = %v, want none — these were registered for another model", calls)
		}
	})

	t.Run("keeps each database's observers apart", func(t *testing.T) {
		t.Parallel()

		var calls []string
		registered := &DB{}
		Observe[tracked](registered, recorder{name: "registered", calls: &calls})

		if err := notify(context.Background(), &DB{}, eventCreated, &tracked{}); err != nil {
			t.Fatalf("notify() error = %v", err)
		}
		if len(calls) != 0 {
			t.Errorf("calls = %v, want none — registration is per database, not per process", calls)
		}
	})

	t.Run("a collection with no database has no observers", func(t *testing.T) {
		t.Parallel()

		if err := notify(context.Background(), nil, eventCreated, &tracked{}); err != nil {
			t.Errorf("notify() error = %v, want nil", err)
		}
	})

	t.Run("rejects something that observes nothing", func(t *testing.T) {
		t.Parallel()

		assertPanics(t, "implements none of", func() {
			Observe[tracked](&DB{}, struct{ Name string }{})
		})
	})

	t.Run("rejects an observer registered for the wrong model", func(t *testing.T) {
		t.Parallel()

		// recorder observes tracked, not User — a mismatch the compiler
		// can't see, since registration takes an any.
		var calls []string
		assertPanics(t, "implements none of", func() {
			Observe[User](&DB{}, recorder{name: "wrong", calls: &calls})
		})
	})

	t.Run("rejects a nil database", func(t *testing.T) {
		t.Parallel()

		assertPanics(t, "db is nil", func() { Observe[tracked](nil) })
	})
}

func TestObserveIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	db := &DB{}
	var calls []string
	Observe[tracked](db, creationOnly{calls: &calls})

	// Registration takes a write lock and dispatch a read lock; the race
	// detector is what actually checks this.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 50 {
			db.observersFor(reflect.TypeFor[tracked]())
		}
	}()
	for range 50 {
		Observe[tracked](db, creationOnly{calls: &calls})
	}
	<-done
}
