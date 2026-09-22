//go:build integration

package odm_test

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/nanaaikinson/chandlery/odm"
)

// reading embeds neither base: its _id is its own, it has no timestamps and
// no state, and the conformance suite should hold it only to that.
type reading struct {
	ID     string `bson:"_id"`
	Sensor string `bson:"sensor"`
	Value  int    `bson:"value"`
}

func (reading) CollectionName() string { return "readings" }

var readingSeq atomic.Int64

// TestModelConformance runs the exported suite over the shapes a model can
// take, which is both a check on those models and a check on the suite:
// each branch it adapts to is exercised by one of them.
func TestModelConformance(t *testing.T) {
	t.Parallel()

	t.Run("odm.Model", func(t *testing.T) {
		t.Parallel()

		odm.TestConformance(t, newUsers(t), func() *user {
			return &user{Name: "Nana", Email: "nana@example.com", Status: "active"}
		})
	})

	t.Run("odm.Model and odm.SoftDeletes", func(t *testing.T) {
		t.Parallel()

		odm.TestConformance(t, newNotes(t), func() *note {
			return &note{Title: "a note", Author: "nana"}
		})
	})

	t.Run("a model declaring indexes", func(t *testing.T) {
		t.Parallel()

		odm.TestConformance(t, newAccounts(t), func() *account {
			// The unique index on email is live once SyncIndexes runs, so
			// every model this hands back needs its own address.
			return &account{Email: fmt.Sprintf("a%d@example.com", readingSeq.Add(1))}
		})
	})

	t.Run("a plain struct with its own _id", func(t *testing.T) {
		t.Parallel()

		collection := odm.Use[reading](odm.New(testDatabase(t, client)))
		odm.TestConformance(t, collection, func() *reading {
			return &reading{
				ID:     fmt.Sprintf("reading-%d", readingSeq.Add(1)),
				Sensor: "temp",
				Value:  21,
			}
		})
	})
}
