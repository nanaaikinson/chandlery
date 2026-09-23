//go:build integration

package odm_test

import (
	"context"
	"testing"

	"github.com/oklog/ulid/v2"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/nanaaikinson/chandlery/odm"
)

// tenant keeps odm.Model's timestamps and dirty tracking but overrides the
// _id type: its own ID field shadows the embedded ObjectID one, in Go and in
// the BSON codec alike, and BeforeCreate is where it gets its value.
type tenant struct {
	odm.Model `bson:",inline"`
	ID        string `bson:"_id" json:"id"`

	Name string `bson:"name"`

	Members []member `bson:"-"`
}

func (tenant) CollectionName() string { return "tenants" }

func (t *tenant) BeforeCreate(_ context.Context) error {
	if t.ID == "" {
		t.ID = ulid.Make().String()
	}
	return nil
}

// member points at a tenant, so its key is a string too: relation keys are
// matched by BSON type as well as value.
type member struct {
	odm.Model `bson:",inline"`

	TenantID string `bson:"tenant_id"`
	Name     string `bson:"name"`

	Tenant *tenant `bson:"-"`
}

func (member) CollectionName() string { return "members" }

var (
	tenantMembers = odm.HasMany[tenant, member]{
		ForeignKey: "tenant_id",
		Attach:     func(t *tenant, m []member) { t.Members = m },
	}
	memberTenant = odm.BelongsTo[member, tenant]{
		ForeignKey: "tenant_id",
		Attach:     func(m *member, t *tenant) { m.Tenant = t },
	}
)

func TestOverriddenIDType(t *testing.T) {
	t.Parallel()

	t.Run("Create stores the hook's string _id", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		tenants := odm.Use[tenant](odm.New(testDatabase(t, client)))

		model := tenant{Name: "acme"}
		if err := tenants.Create(ctx, &model); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if _, err := ulid.Parse(model.ID); err != nil {
			t.Fatalf("ID = %q, want the ULID BeforeCreate assigned: %v", model.ID, err)
		}
		if model.CreatedAt.IsZero() {
			t.Error("CreatedAt left zero, want odm.Model's timestamps to still apply")
		}

		var raw bson.Raw
		if err := tenants.Raw().FindOne(ctx, bson.M{"_id": model.ID}).Decode(&raw); err != nil {
			t.Fatalf("FindOne() error = %v", err)
		}
		if got := raw.Lookup("_id").Type; got != bson.TypeString {
			t.Errorf("stored _id type = %v, want %v", got, bson.TypeString)
		}

		found, err := tenants.Find(ctx, model.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if found.ID != model.ID || found.Name != "acme" {
			t.Errorf("Find() = %+v, want the created tenant", found)
		}
	})

	t.Run("a caller-supplied string ID survives", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		tenants := odm.Use[tenant](odm.New(testDatabase(t, client)))

		model := tenant{ID: "acme", Name: "acme"}
		if err := tenants.Create(ctx, &model); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if model.ID != "acme" {
			t.Errorf("Create() overwrote ID = %q, want %q", model.ID, "acme")
		}
		if _, err := tenants.Find(ctx, "acme"); err != nil {
			t.Errorf("Find() error = %v, want the document stored under its chosen _id", err)
		}
	})

	t.Run("Save updates the same document rather than inserting again", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		tenants := odm.Use[tenant](odm.New(testDatabase(t, client)))

		model := &tenant{Name: "acme"}
		if err := tenants.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		model.Name = "acme corp"
		if err := tenants.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		count, err := tenants.Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if count != 1 {
			t.Errorf("Count() = %d, want 1", count)
		}
		got, err := tenants.Find(ctx, model.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if got.Name != "acme corp" {
			t.Errorf("stored name = %q, want %q", got.Name, "acme corp")
		}
	})

	t.Run("relations match on string keys", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		database := odm.New(testDatabase(t, client))
		tenants := odm.Use[tenant](database)
		members := odm.Use[member](database)

		acme := &tenant{Name: "acme"}
		create(t, tenants, acme)
		create(t, members,
			&member{TenantID: acme.ID, Name: "nana"},
			&member{TenantID: acme.ID, Name: "kwesi"},
		)

		got, err := tenants.With(tenantMembers).Find(ctx, acme.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if len(got.Members) != 2 {
			t.Errorf("Members = %d, want 2", len(got.Members))
		}

		loaded, err := members.With(memberTenant).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		for _, m := range loaded {
			if m.Tenant == nil || m.Tenant.ID != acme.ID {
				t.Errorf("member %q's tenant = %v, want acme", m.Name, m.Tenant)
			}
		}
	})
}
