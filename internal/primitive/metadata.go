package primitive

import (
	"encoding/json"
	"strings"

	"primitivebox/internal/cvr"
)

const (
	SideEffectNone  = "none"
	SideEffectRead  = "read"
	SideEffectWrite = "write"
	SideEffectExec  = "exec"

	SourceSystem = "system"
	SourceApp    = "app"
)

// EnrichSchema normalizes metadata for /primitives responses while keeping
// backward-compatible input/output aliases available for older SDK consumers.
func EnrichSchema(schema Schema) Schema {
	if schema.Namespace == "" && schema.Name != "" {
		if idx := strings.IndexByte(schema.Name, '.'); idx > 0 {
			schema.Namespace = schema.Name[:idx]
		} else {
			schema.Namespace = schema.Name
		}
	}
	if len(schema.InputSchema) == 0 {
		schema.InputSchema = cloneJSON(schema.Input)
	}
	if len(schema.OutputSchema) == 0 {
		schema.OutputSchema = cloneJSON(schema.Output)
	}
	if len(schema.Input) == 0 {
		schema.Input = cloneJSON(schema.InputSchema)
	}
	if len(schema.Output) == 0 {
		schema.Output = cloneJSON(schema.OutputSchema)
	}
	if schema.Source == "" {
		schema.Source = SourceSystem
	}
	if schema.Scope == "" {
		schema.Scope = "workspace"
	}
	if schema.SideEffect == "" {
		schema.SideEffect = defaultSideEffect(schema.Name)
	}
	if !schema.CheckpointRequired {
		schema.CheckpointRequired = defaultCheckpointRequirement(schema.Name, schema.SideEffect)
	}
	if schema.TimeoutMs == 0 {
		schema.TimeoutMs = defaultTimeoutMs(schema.Name)
	}
	if schema.VerifierHint == "" {
		schema.VerifierHint = defaultVerifierHint(schema.Name)
	}
	if schema.Intent == (IntentMetadata{}) {
		schema.Intent = defaultIntentMetadata(schema.Name, schema.SideEffect)
	}
	return schema
}

func defaultIntentMetadata(name, sideEffect string) IntentMetadata {
	switch {
	case strings.HasPrefix(name, "fs.read"),
		strings.HasPrefix(name, "fs.list"),
		strings.HasPrefix(name, "fs.diff"),
		strings.HasPrefix(name, "code.search"),
		strings.HasPrefix(name, "code.symbols"),
		name == "state.list",
		name == "db.schema",
		name == "db.query",
		name == "db.query_readonly",
		name == "browser.goto",
		name == "browser.read",
		name == "browser.extract":
		return IntentMetadata{
			Category:   cvr.IntentQuery,
			SideEffect: sideEffect,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		}
	case strings.HasPrefix(name, "verify."),
		strings.HasPrefix(name, "test."),
		strings.HasPrefix(name, "repo.run_tests"):
		return IntentMetadata{
			Category:   cvr.IntentVerification,
			SideEffect: sideEffect,
			Reversible: true,
			RiskLevel:  cvr.RiskMedium,
		}
	case name == "state.checkpoint":
		return IntentMetadata{
			Category:   cvr.IntentMutation,
			SideEffect: sideEffect,
			Reversible: true,
			RiskLevel:  cvr.RiskLow,
		}
	case name == "state.restore":
		return IntentMetadata{
			Category:   cvr.IntentRollback,
			SideEffect: sideEffect,
			Reversible: true,
			RiskLevel:  cvr.RiskHigh,
		}
	case strings.HasPrefix(name, "fs.write"),
		strings.HasPrefix(name, "macro.safe_edit"),
		strings.HasPrefix(name, "repo.patch"),
		strings.HasPrefix(name, "db.execute"),
		strings.HasPrefix(name, "shell.exec"):
		return IntentMetadata{
			Category:   cvr.IntentMutation,
			SideEffect: sideEffect,
			Reversible: false,
			RiskLevel:  cvr.RiskHigh,
		}
	default:
		return IntentMetadata{
			Category:   cvr.IntentMutation,
			SideEffect: sideEffect,
			Reversible: false,
			RiskLevel:  cvr.RiskHigh,
		}
	}
}

func defaultSideEffect(name string) string {
	switch name {
	case "fs.read", "fs.list", "fs.diff", "code.search", "code.symbols", "state.list", "db.schema", "db.query", "db.query_readonly", "browser.goto", "browser.read", "browser.extract":
		return SideEffectRead
	case "fs.write", "state.restore", "repo.patch_symbol":
		return SideEffectWrite
	case "shell.exec", "verify.test", "verify.command", "test.run", "repo.run_tests", "browser.click", "db.execute":
		return SideEffectExec
	case "state.checkpoint":
		return SideEffectWrite
	default:
		return SideEffectNone
	}
}

func defaultCheckpointRequirement(name, sideEffect string) bool {
	switch name {
	case "state.checkpoint", "state.restore", "state.list", "verify.test", "verify.command", "test.run":
		return false
	}
	return sideEffect == SideEffectWrite
}

func defaultTimeoutMs(name string) int {
	switch name {
	case "shell.exec", "verify.test", "verify.command", "test.run", "repo.run_tests":
		return 120000
	case "repo.patch_symbol":
		return 30000
	default:
		return 15000
	}
}

func defaultVerifierHint(name string) string {
	switch name {
	case "repo.patch_symbol":
		return "repo.run_tests"
	default:
		return ""
	}
}

func cloneJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}
