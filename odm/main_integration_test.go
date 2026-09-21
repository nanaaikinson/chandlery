//go:build integration

// Package odm_test's integration suite runs against a single real MongoDB
// container (via testcontainers-go), started once for the test binary.
// Run with: go test -tags=integration ./odm/...
package odm_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// client is the shared connection every test builds its own database from.
// Unlike this repo's other integration suites, there's no connection env var
// to set here: odm.New takes an already-connected *mongo.Database and never
// builds a client of its own, so the test binary owns that lifecycle
// outright.
var client *mongo.Client

// legacyClient points at a second, older server. UpdateOne compiles a sorted
// update differently either side of MongoDB 8.0 — natively on 8.0, through
// findAndModify before it — and the only way to know both branches work is
// to run them against both servers rather than to assert on which branch was
// chosen.
var legacyClient *mongo.Client

// primaryURI is the replica set's connection string, for the one test that
// needs a client of its own — a command monitor counting queries.
var primaryURI string

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	// A replica set, not a standalone: MongoDB only allows transactions
	// there, and it is closer to what anything using this package runs
	// against anyway.
	primary, uri, cleanup, err := start(ctx, "mongo:8", tcmongo.WithReplicaSet("rs0"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting mongodb 8:", err)
		return 1
	}
	defer cleanup()
	client = primary
	primaryURI = uri

	legacy, _, cleanupLegacy, err := start(ctx, "mongo:7")
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting mongodb 7:", err)
		return 1
	}
	defer cleanupLegacy()
	legacyClient = legacy

	return m.Run()
}

// start brings up one MongoDB container and returns a connected client.
func start(ctx context.Context, image string, opts ...testcontainers.ContainerCustomizer) (*mongo.Client, string, func(), error) {
	container, err := tcmongo.Run(ctx, image, opts...)
	if err != nil {
		return nil, "", nil, fmt.Errorf("starting container: %w", err)
	}

	connStr, err := container.ConnectionString(ctx)
	if err != nil {
		container.Terminate(ctx)
		return nil, "", nil, fmt.Errorf("getting connection string: %w", err)
	}

	connected, err := mongo.Connect(options.Client().ApplyURI(connStr))
	if err != nil {
		container.Terminate(ctx)
		return nil, "", nil, fmt.Errorf("connecting: %w", err)
	}

	// Connect doesn't dial, so without this a server that isn't ready yet
	// would surface as a confusing failure inside the first test instead.
	if err := connected.Ping(ctx, readpref.Primary()); err != nil {
		connected.Disconnect(ctx)
		container.Terminate(ctx)
		return nil, "", nil, fmt.Errorf("pinging: %w", err)
	}

	return connected, connStr, func() {
		connected.Disconnect(ctx)
		container.Terminate(ctx)
	}, nil
}
