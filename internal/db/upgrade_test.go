package db

import (
	"context"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// applyThrough replays the embedded migrations up to and including `last`, and
// records them, so the database looks like an older release's.
func applyThrough(t *testing.T, pool *pgxpool.Pool, last string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, name); err != nil {
			t.Fatal(err)
		}
		if name == last {
			return
		}
	}
	t.Fatalf("migration %s not found", last)
}

// TestUpgradeFromAnOlderSchemaWithData is the path a real deployment takes and
// the one the other migration test never exercises: outstanding migrations
// running against a database that already holds data. A failure here leaves a
// service unable to start, so it is worth more than any single query fix.
//
//	VENDRA_TEST_UPGRADE_DSN=postgres://... go test ./internal/db/
//
// The database must be empty; the test builds the older schema itself.
func TestUpgradeFromAnOlderSchemaWithData(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("VENDRA_TEST_UPGRADE_DSN"))
	if dsn == "" {
		t.Skip("set VENDRA_TEST_UPGRADE_DSN to an empty database to run upgrade tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}

	// Stop at the last migration of the v0.3.0 line.
	applyThrough(t, pool, "006_productivity.sql")

	// Seed what such a deployment holds, including the duplicate approvals that
	// accumulated before submission became idempotent.
	seed := []string{
		`INSERT INTO organizations(id,name,path) VALUES('aaaaaaaa-0000-0000-0000-000000000001','구매본부','/')`,
		`INSERT INTO users(id,email,display_name,password_hash,user_type,status,organization_id) VALUES('bbbbbbbb-0000-0000-0000-000000000001','legacy@vendra.test','기존 사용자','$2a$10$abcdefghijklmnopqrstuv','internal','active','aaaaaaaa-0000-0000-0000-000000000001')`,
		`INSERT INTO suppliers(id,supplier_number,name,business_number,status,organization_id) VALUES('cccccccc-0000-0000-0000-000000000001','SUP-LEGACY','기존 공급사','555-55-55555','active','aaaaaaaa-0000-0000-0000-000000000001')`,
		`INSERT INTO business_objects(id,object_type,number,title,status,supplier_id,owner_id,created_by) VALUES('dddddddd-0000-0000-0000-000000000001','contract','LEGACY-C-1','기존 계약','pending_approval','cccccccc-0000-0000-0000-000000000001','bbbbbbbb-0000-0000-0000-000000000001','bbbbbbbb-0000-0000-0000-000000000001')`,
		`INSERT INTO workflow_definitions(id,name,object_type,enabled,steps,created_by) VALUES('eeeeeeee-0000-0000-0000-000000000001','기존 워크플로','contract',true,'[{"name":"승인","role":"","order":0}]','bbbbbbbb-0000-0000-0000-000000000001')`,
		`INSERT INTO workflow_instances(definition_id,object_type,object_id,requested_by,status,created_at) SELECT 'eeeeeeee-0000-0000-0000-000000000001','contract','dddddddd-0000-0000-0000-000000000001','bbbbbbbb-0000-0000-0000-000000000001','pending', now()+(g||' seconds')::interval FROM generate_series(1,4) g`,
		`INSERT INTO documents(supplier_id,document_type,name,version,storage_path,size,checksum,status,uploaded_by) VALUES('cccccccc-0000-0000-0000-000000000001','contract','기존 문서.pdf',1,'/tmp/x',10,'abc','approved','bbbbbbbb-0000-0000-0000-000000000001')`,
	}
	for _, statement := range seed {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatalf("seed: %v\n%s", err, statement)
		}
	}

	// The upgrade itself.
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("upgrading a populated older database failed: %v", err)
	}

	var pending, superseded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='superseded') FROM workflow_instances`).Scan(&pending, &superseded); err != nil {
		t.Fatalf("read instances: %v", err)
	}
	if pending != 1 || superseded != 3 {
		t.Errorf("duplicate approvals resolved to %d pending / %d superseded, want 1/3", pending, superseded)
	}
	// The surviving approval must be the one that was raised first.
	var survivorIsOldest bool
	if err := pool.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM workflow_instances o, workflow_instances p WHERE p.status='pending' AND o.status='superseded' AND o.created_at < p.created_at)`).Scan(&survivorIsOldest); err != nil {
		t.Fatalf("compare: %v", err)
	}
	if !survivorIsOldest {
		t.Error("a later approval survived while an earlier one was superseded")
	}
	// Data the migrations do not own must be untouched.
	var documents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM documents`).Scan(&documents); err != nil {
		t.Fatalf("read documents: %v", err)
	}
	if documents != 1 {
		t.Errorf("%d documents after the upgrade, want the one that was there", documents)
	}
	// Settings introduced along the way must exist without overwriting others.
	for _, key := range []string{"security.login", "security.password", "maintenance.retention", "workflow.separation_of_duties"} {
		var present bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM settings WHERE key=$1)`, key).Scan(&present); err != nil {
			t.Fatalf("read setting: %v", err)
		}
		if !present {
			t.Errorf("the upgrade did not seed %s", key)
		}
	}
	// Running it again must be a no-op rather than an error.
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("re-running the upgrade failed: %v", err)
	}
}
