package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newPrimitiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "primitive",
		Aliases: []string{"primitives"},
		Short:   "Inspect registered primitives",
	}

	var jsonMode bool

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all registered primitives",
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint := resolveEndpoint(endpointFlag)
			if err := checkServerReachable(endpoint); err != nil {
				return err
			}

			client := newPBClient(endpoint)
			data, err := client.get(cmd.Context(), "/primitives")
			if err != nil {
				return err
			}

			if jsonMode {
				var v any
				if err := json.Unmarshal(data, &v); err != nil {
					return fmt.Errorf("decode primitives response: %w", err)
				}
				printJSON(v)
				return nil
			}

			primitives, err := decodePrimitiveList(data)
			if err != nil {
				return fmt.Errorf("failed to parse primitives: %w", err)
			}

			tw := newTableWriter()
			fmt.Fprintf(tw, "NAME\tSOURCE\tSTATUS\tSIDE EFFECT\tADAPTER\tDESCRIPTION\n")
			for _, p := range primitives {
				source := defaultTableValue(p.Source, "system")
				status := defaultTableValue(p.Status, "built-in")
				adapter := defaultTableValue(p.Adapter, "-")
				sideEffect := defaultTableValue(p.SideEffect, "-")
				description := strings.TrimSpace(p.Description)
				if description == "" {
					description = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", p.Name, source, status, sideEffect, adapter, description)
			}
			tw.Flush()
			return nil
		},
	}
	listCmd.Flags().BoolVar(&jsonMode, "json", false, "Output as JSON")

	var schemaJSON bool
	schemaCmd := &cobra.Command{
		Use:   "schema <name>",
		Short: "Show the schema for a primitive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint := resolveEndpoint(endpointFlag)
			if err := checkServerReachable(endpoint); err != nil {
				return err
			}

			client := newPBClient(endpoint)
			data, err := client.get(cmd.Context(), "/primitives")
			if err != nil {
				return err
			}

			name := args[0]
			raw, source, err := findPrimitiveSchema(data, name)
			if err != nil {
				return err
			}

			if source == "app" {
				appData, err := client.get(cmd.Context(), "/app-primitives")
				if err == nil {
					if appRaw, found := findAppPrimitiveManifest(appData, name); found {
						raw = appRaw
					}
				}
			}

			if schemaJSON {
				fmt.Println(string(raw))
				return nil
			}

			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return fmt.Errorf("decode primitive %q: %w", name, err)
			}
			printJSON(v)
			return nil
		},
	}
	schemaCmd.Flags().BoolVar(&schemaJSON, "json", false, "Output as JSON")

	cmd.AddCommand(listCmd, schemaCmd)
	return cmd
}

type primitiveListEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SideEffect  string `json:"side_effect"`
	Source      string `json:"source"`
	Adapter     string `json:"adapter"`
	Status      string `json:"status"`
}

func decodePrimitiveList(data []byte) ([]primitiveListEntry, error) {
	var primitives []primitiveListEntry
	if err := json.Unmarshal(data, &primitives); err == nil {
		return primitives, nil
	}

	var wrapper struct {
		Primitives json.RawMessage `json:"primitives"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(wrapper.Primitives, &primitives); err != nil {
		return nil, err
	}
	return primitives, nil
}

func findPrimitiveSchema(data []byte, name string) (json.RawMessage, string, error) {
	var primitives []json.RawMessage
	if err := json.Unmarshal(data, &primitives); err != nil {
		var wrapper struct {
			Primitives json.RawMessage `json:"primitives"`
		}
		if err2 := json.Unmarshal(data, &wrapper); err2 != nil {
			return nil, "", fmt.Errorf("failed to parse primitives: %w", err)
		}
		if err3 := json.Unmarshal(wrapper.Primitives, &primitives); err3 != nil {
			return nil, "", fmt.Errorf("failed to parse primitives: %w", err3)
		}
	}

	for _, raw := range primitives {
		var entry struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		if entry.Name == name {
			return raw, entry.Source, nil
		}
	}
	return nil, "", fmt.Errorf("primitive %q not found", name)
}

func findAppPrimitiveManifest(data []byte, name string) (json.RawMessage, bool) {
	var wrapper struct {
		AppPrimitives []json.RawMessage `json:"app_primitives"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, false
	}
	for _, raw := range wrapper.AppPrimitives {
		var entry struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		if entry.Name == name {
			return raw, true
		}
	}
	return nil, false
}

func defaultTableValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
