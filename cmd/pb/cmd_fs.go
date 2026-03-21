package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newFSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fs",
		Short: "File system primitives (ergonomic aliases for pb rpc fs.*)",
	}

	var sandboxID string
	var jsonMode bool

	readCmd := &cobra.Command{
		Use:   "read <path>",
		Short: "Read file content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint := resolveEndpoint(endpointFlag)
			if err := checkServerReachable(endpoint); err != nil {
				return err
			}

			client := newPBClient(endpoint)
			resp, err := client.rpcCall(cmd.Context(), "fs.read", map[string]any{"path": args[0]}, sandboxID)
			if err != nil {
				return err
			}
			if resp.Error != nil {
				return fmt.Errorf("error: %s", resp.Error.Message)
			}
			if jsonMode {
				printRawJSON(resp.Result)
				return nil
			}
			// Extract content field for direct output
			var result struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				return fmt.Errorf("decode fs.read response: %w", err)
			}
			fmt.Print(result.Content)
			return nil
		},
	}
	readCmd.Flags().StringVar(&sandboxID, "sandbox", "", "Target sandbox ID")
	readCmd.Flags().BoolVar(&jsonMode, "json", false, "Output as JSON")

	var writeContent string
	writeCmd := &cobra.Command{
		Use:   "write <path>",
		Short: "Write file content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint := resolveEndpoint(endpointFlag)
			if err := checkServerReachable(endpoint); err != nil {
				return err
			}

			client := newPBClient(endpoint)
			resp, err := client.rpcCall(cmd.Context(), "fs.write", map[string]any{
				"path":    args[0],
				"content": writeContent,
			}, sandboxID)
			if err != nil {
				return err
			}
			if resp.Error != nil {
				return fmt.Errorf("error: %s", resp.Error.Message)
			}
			fmt.Println("Written successfully.")
			return nil
		},
	}
	writeCmd.Flags().StringVar(&writeContent, "content", "", "File content to write")
	writeCmd.Flags().StringVar(&sandboxID, "sandbox", "", "Target sandbox ID")
	_ = writeCmd.MarkFlagRequired("content")

	var listSandboxID string
	var listJSON bool
	listCmd := &cobra.Command{
		Use:   "list [path]",
		Short: "List directory contents",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint := resolveEndpoint(endpointFlag)
			if err := checkServerReachable(endpoint); err != nil {
				return err
			}

			path := "."
			if len(args) > 0 {
				path = args[0]
			}

			client := newPBClient(endpoint)
			resp, err := client.rpcCall(cmd.Context(), "fs.list", map[string]any{"path": path}, listSandboxID)
			if err != nil {
				return err
			}
			if resp.Error != nil {
				return fmt.Errorf("error: %s", resp.Error.Message)
			}
			if listJSON {
				printRawJSON(resp.Result)
				return nil
			}
			// Extract entries for display
			var result struct {
				Entries []struct {
					Name string `json:"name"`
					Type string `json:"type"`
					Size int64  `json:"size"`
				} `json:"entries"`
			}
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				printRawJSON(resp.Result)
				return nil
			}
			for _, e := range result.Entries {
				if e.Type == "directory" {
					fmt.Printf("%s/\n", e.Name)
				} else {
					fmt.Println(e.Name)
				}
			}
			return nil
		},
	}
	listCmd.Flags().StringVar(&listSandboxID, "sandbox", "", "Target sandbox ID")
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Output as JSON")

	cmd.AddCommand(readCmd, writeCmd, listCmd)
	return cmd
}
