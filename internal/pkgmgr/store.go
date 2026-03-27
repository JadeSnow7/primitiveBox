package pkgmgr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotInstalled is returned when a package is not installed.
var ErrNotInstalled = errors.New("package not installed")

// InstalledPackage represents a package that has been installed.
type InstalledPackage struct {
	Name        string
	Version     string
	InstalledAt time.Time
	SocketPath  string
	BinaryPath  string
	Args        []string
	Status      string
}

// PackageStore persists installed package state.
type PackageStore interface {
	Save(ctx context.Context, pkg InstalledPackage) error
	Remove(ctx context.Context, name string) error
	Get(ctx context.Context, name string) (*InstalledPackage, error)
	List(ctx context.Context) ([]InstalledPackage, error)
	SetStatus(ctx context.Context, name, status string) error
}

// SQLitePackageStore persists package state in a SQLite database.
type SQLitePackageStore struct {
	db *sql.DB
}

// NewSQLitePackageStore opens the package store backed by the given *sql.DB.
// It creates the installed_packages table if it does not already exist.
func NewSQLitePackageStore(db *sql.DB) (*SQLitePackageStore, error) {
	s := &SQLitePackageStore{db: db}
	if err := s.init(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SQLitePackageStore) init() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS installed_packages (
		name         TEXT PRIMARY KEY,
		version      TEXT NOT NULL,
		installed_at INTEGER NOT NULL,
		socket_path  TEXT NOT NULL,
		binary_path  TEXT NOT NULL,
		args_json    TEXT NOT NULL DEFAULT '[]',
		status       TEXT NOT NULL DEFAULT 'installed'
	)`)
	return err
}

// Save upserts an installed package record.
func (s *SQLitePackageStore) Save(ctx context.Context, pkg InstalledPackage) error {
	argsJSON, err := json.Marshal(pkg.Args)
	if err != nil {
		return fmt.Errorf("marshal args: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO installed_packages (name, version, installed_at, socket_path, binary_path, args_json, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			version=excluded.version,
			installed_at=excluded.installed_at,
			socket_path=excluded.socket_path,
			binary_path=excluded.binary_path,
			args_json=excluded.args_json,
			status=excluded.status
	`, pkg.Name, pkg.Version, pkg.InstalledAt.Unix(), pkg.SocketPath, pkg.BinaryPath, string(argsJSON), pkg.Status)
	if err != nil {
		return fmt.Errorf("upsert installed package: %w", err)
	}
	return tx.Commit()
}

// Remove deletes a package record by name.
func (s *SQLitePackageStore) Remove(ctx context.Context, name string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remove tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM installed_packages WHERE name = ?`, name); err != nil {
		return fmt.Errorf("delete installed package: %w", err)
	}
	return tx.Commit()
}

// Get returns the installed package by name, or nil if not installed.
func (s *SQLitePackageStore) Get(ctx context.Context, name string) (*InstalledPackage, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT name, version, installed_at, socket_path, binary_path, args_json, status
		FROM installed_packages WHERE name = ?
	`, name)
	pkg, err := scanInstalledPackage(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return pkg, nil
}

// List returns all installed packages ordered by name.
func (s *SQLitePackageStore) List(ctx context.Context) ([]InstalledPackage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, version, installed_at, socket_path, binary_path, args_json, status
		FROM installed_packages ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list installed packages: %w", err)
	}
	defer rows.Close()

	var result []InstalledPackage
	for rows.Next() {
		pkg, err := scanInstalledPackage(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, *pkg)
	}
	return result, rows.Err()
}

// SetStatus updates the status field for the named package.
func (s *SQLitePackageStore) SetStatus(ctx context.Context, name, status string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin status tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE installed_packages SET status = ? WHERE name = ?`, status, name); err != nil {
		return fmt.Errorf("set package status: %w", err)
	}
	return tx.Commit()
}

func scanInstalledPackage(scan func(dest ...any) error) (*InstalledPackage, error) {
	var (
		name        string
		version     string
		installedAt int64
		socketPath  string
		binaryPath  string
		argsJSON    string
		status      string
	)
	if err := scan(&name, &version, &installedAt, &socketPath, &binaryPath, &argsJSON, &status); err != nil {
		return nil, err
	}
	pkg := &InstalledPackage{
		Name:        name,
		Version:     version,
		InstalledAt: time.Unix(installedAt, 0).UTC(),
		SocketPath:  socketPath,
		BinaryPath:  binaryPath,
		Status:      status,
	}
	if argsJSON != "" && argsJSON != "null" {
		if err := json.Unmarshal([]byte(argsJSON), &pkg.Args); err != nil {
			return nil, fmt.Errorf("unmarshal args for %s: %w", name, err)
		}
	}
	return pkg, nil
}
