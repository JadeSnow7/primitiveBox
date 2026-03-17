package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newPrimitiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "primitive",
		Short: "Inspect registered primitives",
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
				json.Unmarshal(data, &v)
				printJSON(v)
				return nil
			}

			var primitives []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				SideEffect  string `json:"side_effect"`
			}
			if err := json.Unmarshal(data, &primitives); err != nil {
				// Try wrapper format
				var wrapper struct {
					Primitives json.RawMessage `json:"primitives"`
				}
				if err2 := json.Unmarshal(data, &wrapper); err2 == nil {
					json.Unmarshal(wrapper.Primitives, &primitives)
				} else {
					return fmt.Errorf("failed to parse primitives: %w", err)
				}
			}

			tw := newTableWriter()
			fmt.Fprintf(tw, "NAME\tSIDE EFFECT\tDESCRIPTION\n")
			for _, p := range primitives {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, p.SideEffect, p.Description)
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
			var primitives []json.RawMessage

			// Try array directly or wrapped
			if err := json.Unmarshal(data, &primitives); err != nil {
				var wrapper struct {
					Primitives json.RawMessage `json:"primitives"`
				}
				if err2 := json.Unmarshal(data, &wrapper); err2 == nil {
					json.Unmarshal(wrapper.Primitives, &primitives)
				}
			}

			for _, raw := range primitives {
				var entry struct {
					Name string `json:"name"`
				}
				json.Unmarshal(raw, &entry)
				if entry.Name == name {
					if schemaJSON {
						fmt.Println(string(raw))
					} else {
						var v any
						json.Unmarshal(raw, &v)
						printJSON(v)
					}
					return nil
				}
			}
			return fmt.Errorf("primitive %q not found", name)
		},
	}
	schemaCmd.Flags().BoolVar(&schemaJSON, "json", false, "Output as JSON")

	cmd.AddCommand(listCmd, schemaCmd)
	return cmd
}
