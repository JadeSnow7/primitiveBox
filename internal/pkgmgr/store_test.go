package pkgmgr

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSQLitePackageStore_SaveAndGet(t *testing.T) {
	db := openTestDB(t)
	store, err := NewSQLitePackageStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePackageStore: %v", err)
	}

	ctx := context.Background()
	pkg := InstalledPackage{
		Name:        "os",
		Version:     "0.1.0",
		InstalledAt: time.Now().UTC().Truncate(time.Second),
		SocketPath:  "/tmp/os-adapter.sock",
		BinaryPath:  "/usr/local/bin/pb-os-adapter",
		Args:        []string{"--socket", "/tmp/os-adapter.sock"},
		Status:      "active",
	}

	if err := store.Save(ctx, pkg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ctx, "os")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Name != pkg.Name {
		t.Errorf("Name: got %q, want %q", got.Name, pkg.Name)
	}
	if got.Version != pkg.Version {
		t.Errorf("Version: got %q, want %q", got.Version, pkg.Version)
	}
	if got.Status != pkg.Status {
		t.Errorf("Status: got %q, want %q", got.Status, pkg.Status)
	}
	if len(got.Args) != len(pkg.Args) {
		t.Errorf("Args len: got %d, want %d", len(got.Args), len(pkg.Args))
	}
}

func TestSQLitePackageStore_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	store, err := NewSQLitePackageStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePackageStore: %v", err)
	}

	ctx := context.Background()
	got, err := store.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestSQLitePackageStore_Upsert(t *testing.T) {
	db := openTestDB(t)
	store, err := NewSQLitePackageStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePackageStore: %v", err)
	}

	ctx := context.Background()
	pkg := InstalledPackage{
		Name:        "os",
		Version:     "0.1.0",
		InstalledAt: time.Now().UTC().Truncate(time.Second),
		SocketPath:  "/tmp/os-adapter.sock",
		BinaryPath:  "/usr/local/bin/pb-os-adapter",
		Args:        nil,
		Status:      "installed",
	}

	if err := store.Save(ctx, pkg); err != nil {
		t.Fatalf("first Save: %v", err)
	}

	pkg.Version = "0.2.0"
	pkg.Status = "active"
	if err := store.Save(ctx, pkg); err != nil {
		t.Fatalf("second Save (upsert): %v", err)
	}

	got, err := store.Get(ctx, "os")
	if err != nil {
		t.Fatalf("Get after upsert: %v", err)
	}
	if got.Version != "0.2.0" {
		t.Errorf("Version after upsert: got %q, want 0.2.0", got.Version)
	}
	if got.Status != "active" {
		t.Errorf("Status after upsert: got %q, want active", got.Status)
	}
}

func TestSQLitePackageStore_Remove(t *testing.T) {
	db := openTestDB(t)
	store, err := NewSQLitePackageStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePackageStore: %v", err)
	}

	ctx := context.Background()
	pkg := InstalledPackage{
		Name:        "os",
		Version:     "0.1.0",
		InstalledAt: time.Now().UTC().Truncate(time.Second),
		SocketPath:  "/tmp/os-adapter.sock",
		BinaryPath:  "/usr/local/bin/pb-os-adapter",
		Status:      "active",
	}

	if err := store.Save(ctx, pkg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Remove(ctx, "os"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	got, err := store.Get(ctx, "os")
	if err != nil {
		t.Fatalf("Get after remove: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil after remove, got %+v", got)
	}
}

func TestSQLitePackageStore_List(t *testing.T) {
	db := openTestDB(t)
	store, err := NewSQLitePackageStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePackageStore: %v", err)
	}

	ctx := context.Background()
	for _, name := range []string{"mcp-bridge", "os"} {
		pkg := InstalledPackage{
			Name:        name,
			Version:     "0.1.0",
			InstalledAt: time.Now().UTC().Truncate(time.Second),
			SocketPath:  "/tmp/" + name + ".sock",
			BinaryPath:  "/usr/local/bin/pb-" + name,
			Status:      "active",
		}
		if err := store.Save(ctx, pkg); err != nil {
			t.Fatalf("Save %q: %v", name, err)
		}
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List len: got %d, want 2", len(list))
	}
	// Should be ordered by name ASC: mcp-bridge, os
	if list[0].Name != "mcp-bridge" {
		t.Errorf("list[0].Name: got %q, want mcp-bridge", list[0].Name)
	}
	if list[1].Name != "os" {
		t.Errorf("list[1].Name: got %q, want os", list[1].Name)
	}
}

func TestSQLitePackageStore_SetStatus(t *testing.T) {
	db := openTestDB(t)
	store, err := NewSQLitePackageStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePackageStore: %v", err)
	}

	ctx := context.Background()
	pkg := InstalledPackage{
		Name:        "os",
		Version:     "0.1.0",
		InstalledAt: time.Now().UTC().Truncate(time.Second),
		SocketPath:  "/tmp/os.sock",
		BinaryPath:  "/usr/local/bin/pb-os-adapter",
		Status:      "installed",
	}

	if err := store.Save(ctx, pkg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.SetStatus(ctx, "os", "active"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	got, err := store.Get(ctx, "os")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "active" {
		t.Errorf("Status: got %q, want active", got.Status)
	}
}

func TestSQLitePackageStore_NilArgs(t *testing.T) {
	db := openTestDB(t)
	store, err := NewSQLitePackageStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePackageStore: %v", err)
	}

	ctx := context.Background()
	pkg := InstalledPackage{
		Name:        "os",
		Version:     "0.1.0",
		InstalledAt: time.Now().UTC().Truncate(time.Second),
		SocketPath:  "/tmp/os.sock",
		BinaryPath:  "/usr/local/bin/pb-os-adapter",
		Args:        nil,
		Status:      "active",
	}

	if err := store.Save(ctx, pkg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ctx, "os")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// nil args should round-trip as nil or empty slice
	if len(got.Args) != 0 {
		t.Errorf("expected nil/empty Args, got %v", got.Args)
	}
}
