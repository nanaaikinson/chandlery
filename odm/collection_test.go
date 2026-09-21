package odm

import (
	"errors"
	"strings"
	"testing"
)

// namedByValue names its collection from a value receiver.
type namedByValue struct{}

func (namedByValue) CollectionName() string { return "explicitly_named" }

// namedByPointer names its collection from a pointer receiver, which a
// value of the type doesn't satisfy — resolution has to check *T too.
type namedByPointer struct{}

func (*namedByPointer) CollectionName() string { return "pointer_named" }

// User has no CollectionName, so it exercises the fallback.
type User struct {
	Model `bson:",inline"`

	Name     string `bson:"name"`
	Email    string `bson:"email"`
	IsActive bool   `bson:"is_active"`
}

func TestCollectionName(t *testing.T) {
	t.Parallel()

	t.Run("uses CollectionName with a value receiver", func(t *testing.T) {
		t.Parallel()
		if got := metaFor[namedByValue]().collection; got != "explicitly_named" {
			t.Errorf("collectionName() = %q, want %q", got, "explicitly_named")
		}
	})

	t.Run("uses CollectionName with a pointer receiver", func(t *testing.T) {
		t.Parallel()
		if got := metaFor[namedByPointer]().collection; got != "pointer_named" {
			t.Errorf("collectionName() = %q, want %q", got, "pointer_named")
		}
	})

	t.Run("falls back to the lowercased type name plus s", func(t *testing.T) {
		t.Parallel()
		if got := metaFor[User]().collection; got != "users" {
			t.Errorf("collectionName() = %q, want %q", got, "users")
		}
	})

	t.Run("returns the same name when resolved again from cache", func(t *testing.T) {
		t.Parallel()
		first := metaFor[User]().collection
		if second := metaFor[User]().collection; second != first {
			t.Errorf("collectionName() = %q on the second call, want %q", second, first)
		}
	})

	t.Run("panics on a pointer model type", func(t *testing.T) {
		t.Parallel()
		assertPanics(t, "not a pointer", func() { _ = metaFor[*User]().collection })
	})

	t.Run("panics on a non-struct model type with no CollectionName", func(t *testing.T) {
		t.Parallel()
		assertPanics(t, "must be a struct", func() { _ = metaFor[int]().collection })
	})

	t.Run("panics on an anonymous struct", func(t *testing.T) {
		t.Parallel()
		assertPanics(t, "anonymous struct", func() { _ = metaFor[struct{ Name string }]().collection })
	})
}

func TestUsePanicsOnNilDB(t *testing.T) {
	t.Parallel()

	assertPanics(t, "db is nil", func() { Use[User](nil) })
}

func TestNewPanicsOnNilDatabase(t *testing.T) {
	t.Parallel()

	assertPanics(t, "database is nil", func() { New(nil) })
}

func TestCreateNilModel(t *testing.T) {
	t.Parallel()

	if err := testCollection().Create(t.Context(), nil); !errors.Is(err, ErrNilModel) {
		t.Errorf("Create(nil) error = %v, want ErrNilModel", err)
	}
}

// assertPanics runs fn and fails unless it panics with a message containing
// want.
func assertPanics(t *testing.T, want string, fn func()) {
	t.Helper()

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("did not panic, want a panic containing %q", want)
		}
		message, ok := recovered.(string)
		if !ok {
			t.Fatalf("panicked with %T (%v), want a string containing %q", recovered, recovered, want)
		}
		if !strings.Contains(message, want) {
			t.Errorf("panicked with %q, want it to contain %q", message, want)
		}
	}()

	fn()
}
