package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"primitivebox/internal/cvr"
	"primitivebox/internal/eventing"
	"primitivebox/internal/primitive"
)

type SQLiteAppRegistry struct {
	store *SQLiteStore
	bus   *eventing.Bus
}

func NewSQLiteAppRegistry(store *SQLiteStore, bus *eventing.Bus) *SQLiteAppRegistry {
	return &SQLiteAppRegistry{store: store, bus: bus}
}

func (r *SQLiteAppRegistry) Register(ctx context.Context, manifest primitive.AppPrimitiveManifest) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("app registry unavailable")
	}

	normalized, err := primitive.NormalizeAppPrimitiveManifest(manifest)
	if err != nil {
		return err
	}

	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin app registration tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT name, app_id, availability
		FROM app_primitives
		WHERE app_id = ? OR name = ?
	`, normalized.AppID, normalized.Name)
	if err != nil {
		return fmt.Errorf("query existing app registrations: %w", err)
	}
	defer rows.Close()

	reactivating := false
	for rows.Next() {
		var (
			name         string
			appID        string
			availability string
		)
		if err := rows.Scan(&name, &appID, &availability); err != nil {
			return fmt.Errorf("scan app registration: %w", err)
		}
		if name == normalized.Name && appID != normalized.AppID {
			return fmt.Errorf(
				"app_primitive_conflict: %q is already registered by app %q",
				normalized.Name,
				appID,
			)
		}
		if appID == normalized.AppID && primitive.AppPrimitiveAvailability(availability) == primitive.AppPrimitiveUnavailable {
			reactivating = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate app registrations: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE app_primitives
		SET availability = ?, updated_at = ?
		WHERE app_id = ?
	`, string(primitive.AppPrimitiveActive), time.Now().UTC().Unix(), normalized.AppID); err != nil {
		return fmt.Errorf("reactivate app registrations: %w", err)
	}

	verifyJSON, err := json.Marshal(normalized.Verify)
	if err != nil {
		return fmt.Errorf("marshal verify declaration: %w", err)
	}
	rollbackJSON, err := json.Marshal(normalized.Rollback)
	if err != nil {
		return fmt.Errorf("marshal rollback declaration: %w", err)
	}
	intentJSON, err := json.Marshal(normalized.Intent)
	if err != nil {
		return fmt.Errorf("marshal primitive intent: %w", err)
	}
	now := time.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO app_primitives (
			name, app_id, description, input_schema_json, output_schema_json, ui_layout_hint, socket_path,
			availability, verify_endpoint, verify_json, rollback_endpoint, rollback_json,
			intent_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			app_id = excluded.app_id,
			description = excluded.description,
			input_schema_json = excluded.input_schema_json,
			output_schema_json = excluded.output_schema_json,
			ui_layout_hint = excluded.ui_layout_hint,
			socket_path = excluded.socket_path,
			availability = excluded.availability,
			verify_endpoint = excluded.verify_endpoint,
			verify_json = excluded.verify_json,
			rollback_endpoint = excluded.rollback_endpoint,
			rollback_json = excluded.rollback_json,
			intent_json = excluded.intent_json,
			updated_at = excluded.updated_at
	`, normalized.Name, normalized.AppID, normalized.Description, string(normalized.InputSchema), string(normalized.OutputSchema),
		normalized.UILayoutHint, normalized.SocketPath, string(primitive.AppPrimitiveActive), normalized.VerifyEndpoint, string(verifyJSON),
		normalized.RollbackEndpoint, string(rollbackJSON), string(intentJSON), now); err != nil {
		return fmt.Errorf("upsert app registration: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit app registration: %w", err)
	}

	normalized.Availability = primitive.AppPrimitiveActive
	eventType := "adapter.registered"
	if reactivating {
		eventType = "adapter.reactivated"
	}
	r.publish(ctx, eventType, normalized.AppID, normalized)
	return nil
}

func (r *SQLiteAppRegistry) Unregister(ctx context.Context, appID, name string) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("app registry unavailable")
	}
	if _, err := r.store.db.ExecContext(ctx, `
		DELETE FROM app_primitives
		WHERE app_id = ? AND name = ?
	`, appID, name); err != nil {
		return fmt.Errorf("delete app registration: %w", err)
	}
	return nil
}

func (r *SQLiteAppRegistry) Get(ctx context.Context, name string) (*primitive.AppPrimitiveManifest, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("app registry unavailable")
	}
	row := r.store.db.QueryRowContext(ctx, `
		SELECT app_id, name, description, input_schema_json, output_schema_json, ui_layout_hint, socket_path,
		       availability, verify_endpoint, verify_json, rollback_endpoint, rollback_json,
		       intent_json
		FROM app_primitives
		WHERE name = ?
	`, name)
	manifest, err := scanAppPrimitiveManifest(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return manifest, nil
}

func (r *SQLiteAppRegistry) List(ctx context.Context) ([]primitive.AppPrimitiveManifest, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("app registry unavailable")
	}
	rows, err := r.store.db.QueryContext(ctx, `
		SELECT app_id, name, description, input_schema_json, output_schema_json, ui_layout_hint, socket_path,
		       availability, verify_endpoint, verify_json, rollback_endpoint, rollback_json,
		       intent_json
		FROM app_primitives
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list app registrations: %w", err)
	}
	defer rows.Close()

	items := make([]primitive.AppPrimitiveManifest, 0)
	for rows.Next() {
		manifest, err := scanAppPrimitiveManifest(rows.Scan)
		if err != nil {
			return nil, err
		}
		items = append(items, *manifest)
	}
	return items, rows.Err()
}

func (r *SQLiteAppRegistry) MarkUnavailable(ctx context.Context, appID string) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("app registry unavailable")
	}

	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin app availability tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT app_id, name, description, input_schema_json, output_schema_json, ui_layout_hint, socket_path,
		       availability, verify_endpoint, verify_json, rollback_endpoint, rollback_json,
		       intent_json
		FROM app_primitives
		WHERE app_id = ?
	`, appID)
	if err != nil {
		return fmt.Errorf("query app registrations for unavailability: %w", err)
	}
	defer rows.Close()

	manifests := make([]primitive.AppPrimitiveManifest, 0)
	alreadyUnavailable := true
	for rows.Next() {
		manifest, err := scanAppPrimitiveManifest(rows.Scan)
		if err != nil {
			return err
		}
		if manifest.Availability != primitive.AppPrimitiveUnavailable {
			alreadyUnavailable = false
		}
		manifest.Availability = primitive.AppPrimitiveUnavailable
		manifests = append(manifests, *manifest)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate app registrations for unavailability: %w", err)
	}
	if len(manifests) == 0 || alreadyUnavailable {
		return nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE app_primitives
		SET availability = ?, updated_at = ?
		WHERE app_id = ?
	`, string(primitive.AppPrimitiveUnavailable), time.Now().UTC().Unix(), appID); err != nil {
		return fmt.Errorf("update app availability: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit app availability: %w", err)
	}

	sort.Slice(manifests, func(i, j int) bool { return manifests[i].Name < manifests[j].Name })
	r.publish(ctx, "adapter.unavailable", appID, map[string]any{
		"app_id":     appID,
		"status":     primitive.AppPrimitiveUnavailable,
		"primitives": manifests,
	})
	return nil
}

func (r *SQLiteAppRegistry) publish(ctx context.Context, eventType, appID string, payload any) {
	if r == nil || r.bus == nil {
		return
	}
	r.bus.Publish(ctx, eventing.Event{
		Type:    eventType,
		Source:  "adapter",
		Message: appID,
		Data:    eventing.MustJSON(payload),
	})
}

func scanAppPrimitiveManifest(scan func(dest ...any) error) (*primitive.AppPrimitiveManifest, error) {
	var (
		appID            string
		name             string
		description      string
		inputSchemaJSON  string
		outputSchemaJSON string
		uiLayoutHint     string
		socketPath       string
		availability     string
		verifyEndpoint   string
		verifyJSON       string
		rollbackEndpoint string
		rollbackJSON     string
		intentJSON       string
	)
	if err := scan(
		&appID,
		&name,
		&description,
		&inputSchemaJSON,
		&outputSchemaJSON,
		&uiLayoutHint,
		&socketPath,
		&availability,
		&verifyEndpoint,
		&verifyJSON,
		&rollbackEndpoint,
		&rollbackJSON,
		&intentJSON,
	); err != nil {
		return nil, err
	}

	manifest := &primitive.AppPrimitiveManifest{
		AppID:            appID,
		Name:             name,
		Description:      description,
		InputSchema:      json.RawMessage(inputSchemaJSON),
		OutputSchema:     json.RawMessage(outputSchemaJSON),
		UILayoutHint:     uiLayoutHint,
		SocketPath:       socketPath,
		Availability:     primitive.AppPrimitiveAvailability(availability),
		VerifyEndpoint:   verifyEndpoint,
		RollbackEndpoint: rollbackEndpoint,
	}
	if verifyJSON != "" && verifyJSON != "null" {
		manifest.Verify = &primitive.AppPrimitiveVerify{}
		if err := json.Unmarshal([]byte(verifyJSON), manifest.Verify); err != nil {
			return nil, fmt.Errorf("decode verify declaration for %s: %w", name, err)
		}
	}
	if rollbackJSON != "" && rollbackJSON != "null" {
		manifest.Rollback = &primitive.AppPrimitiveRollback{}
		if err := json.Unmarshal([]byte(rollbackJSON), manifest.Rollback); err != nil {
			return nil, fmt.Errorf("decode rollback declaration for %s: %w", name, err)
		}
	}
	if intentJSON != "" {
		if err := json.Unmarshal([]byte(intentJSON), &manifest.Intent); err != nil {
			return nil, fmt.Errorf("decode intent declaration for %s: %w", name, err)
		}
	} else {
		manifest.Intent = cvr.PrimitiveIntent{}
	}
	return manifest, nil
}
