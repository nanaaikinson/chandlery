package odm

import (
	"context"
	"sync"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// wireVersionMongoDB80 is the maximum wire version MongoDB 8.0 reports, and
// the release that first accepts a sort on updateOne. Earlier servers reject
// the field outright ("BSON field 'update.updates.sort' is an unknown
// field"), so a sorted UpdateOne has to take the findAndModify path there
// instead. Wire version, rather than a parsed version string, because hello
// is the one command every deployment answers — unauthenticated, on every
// topology — while buildInfo can need privileges a caller may not have.
const wireVersionMongoDB80 = 25

// sortableUpdateOne reports whether a server at this wire version accepts a
// sorted updateOne. Split out from the probe so the threshold itself is
// testable without a server.
func sortableUpdateOne(maxWireVersion int) bool {
	return maxWireVersion >= wireVersionMongoDB80
}

// serverCapabilities caches what the connected server supports. The probe is
// one round trip, made on first need and never repeated — never at New,
// which stays free of I/O.
type serverCapabilities struct {
	mu             sync.Mutex
	maxWireVersion int
	known          bool
}

// supportsSortedUpdateOne reports whether this database's server accepts a
// sort on updateOne, probing once and caching the answer.
//
// A failed probe answers false rather than returning an error: the
// findAndModify path it selects is correct on every server version, so
// falling back costs only the ModifiedCount fidelity noted on UpdateOne, and
// a capability check has no business failing a caller's update. The failure
// isn't cached either, so a transient one doesn't pin every later call to
// the fallback.
func (db *DB) supportsSortedUpdateOne(ctx context.Context) bool {
	db.capabilities.mu.Lock()
	known, version := db.capabilities.known, db.capabilities.maxWireVersion
	db.capabilities.mu.Unlock()

	if known {
		return sortableUpdateOne(version)
	}

	version, err := db.maxWireVersion(ctx)
	if err != nil {
		return false
	}

	db.capabilities.mu.Lock()
	db.capabilities.maxWireVersion, db.capabilities.known = version, true
	db.capabilities.mu.Unlock()

	return sortableUpdateOne(version)
}

func (db *DB) maxWireVersion(ctx context.Context) (int, error) {
	var reply struct {
		MaxWireVersion int32 `bson:"maxWireVersion"`
	}
	if err := db.database.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&reply); err != nil {
		return 0, err
	}
	return int(reply.MaxWireVersion), nil
}
