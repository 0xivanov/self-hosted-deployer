package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/db/migrations"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func TestSQLiteMigrationCompatibilityPreservesLegacyState(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "compatibility.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, db, ".", 1); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}
	created := "2026-09-06T10:00:00Z"
	if _, err := db.ExecContext(ctx, `INSERT INTO apps (id, name, image, desired_state_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"app-legacy", "legacy-api", "example/legacy-api:1.2.3", `{"name":"legacy-api","image":"example/legacy-api:1.2.3"}`, created, created); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO routes (id, app_id, domain, target_port, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"route-legacy", "app-legacy", "legacy.example.test", 8080, created, created); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO nodes (id, name, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		"node-legacy", "legacy-node", "online", created, created); err != nil {
		t.Fatal(err)
	}

	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("upgrade schema: %v", err)
	}
	upgraded := New(db)
	app, err := NewAppRepository(upgraded).FindActiveByName(ctx, "legacy-api")
	if err != nil || app.Image != "example/legacy-api:1.2.3" || app.DesiredStateJSON == "" {
		t.Fatalf("legacy app was not readable after upgrade: %#v, %v", app, err)
	}
	route, err := NewRouteRepository(upgraded).FindByDomain(ctx, "legacy.example.test")
	if err != nil || route.TargetPort != 8080 || route.Status != "pending" || route.TLSEnabled {
		t.Fatalf("legacy route changed after upgrade: %#v, %v", route, err)
	}
	node, err := NewNodeRepository(upgraded).FindByID(ctx, "node-legacy")
	if err != nil || node.Status != "online" || node.WireGuardIP != "" || node.VPNStatus != "" {
		t.Fatalf("legacy node changed after upgrade: %#v, %v", node, err)
	}

	// A previous binary only selects columns from migration 1. Verify that its
	// read path remains valid while the upgraded schema is installed.
	var oldName, oldImage string
	if err := db.QueryRowContext(ctx, `SELECT name, image FROM apps WHERE id = ?`, "app-legacy").Scan(&oldName, &oldImage); err != nil {
		t.Fatalf("legacy read query on upgraded schema: %v", err)
	}
	if oldName != "legacy-api" || oldImage != "example/legacy-api:1.2.3" {
		t.Fatalf("legacy read returned %#v %q", oldName, oldImage)
	}

	if err := goose.DownToContext(ctx, db, ".", 1); err != nil {
		t.Fatalf("rollback schema: %v", err)
	}
	var retained int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM apps WHERE id = ?`, "app-legacy").Scan(&retained); err != nil {
		t.Fatalf("read retained app after rollback: %v", err)
	}
	if retained != 1 {
		t.Fatalf("rollback discarded persisted app state: count=%d", retained)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("reapply schema after rollback: %v", err)
	}
	if _, err := NewAppRepository(New(db)).FindActiveByName(ctx, "legacy-api"); err != nil {
		t.Fatalf("legacy app unreadable after rollback and reapply: %v", err)
	}
}
