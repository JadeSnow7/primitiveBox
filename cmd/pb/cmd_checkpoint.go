package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newCheckpointCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checkpoint",
		Short: "Checkpoint management (aliases for state.* primitives)",
	}

	var sandboxID string

	var label string
	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a checkpoint",
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint := resolveEndpoint(endpointFlag)
			if err := checkServerReachable(endpoint); err != nil {
				return err
			}

			params := map[string]any{}
			if label != "" {
				params["label"] = label
			}

			client := newPBClient(endpoint)
			resp, err := client.rpcCall(cmd.Context(), "state.checkpoint", params, sandboxID)
			if err != nil {
				return err
			}
			if resp.Error != nil {
				return fmt.Errorf("error: %s", resp.Error.Message)
			}

			var result struct {
				CheckpointID string `json:"checkpoint_id"`
			}
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				return fmt.Errorf("decode checkpoint response: %w", err)
			}
			fmt.Printf("Checkpoint created: %s\n", result.CheckpointID)
			return nil
		},
	}
	createCmd.Flags().StringVar(&label, "label", "", "Checkpoint label")
	createCmd.Flags().StringVar(&sandboxID, "sandbox", "", "Target sandbox ID")

	var listJSON bool
	var listSandboxID string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List checkpoints",
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint := resolveEndpoint(endpointFlag)
			if err := checkServerReachable(endpoint); err != nil {
				return err
			}

			client := newPBClient(endpoint)
			resp, err := client.rpcCall(cmd.Context(), "state.list", map[string]any{}, listSandboxID)
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

			var result struct {
				Checkpoints []struct {
					ID      string `json:"id"`
					Label   string `json:"label"`
					Created string `json:"created"`
				} `json:"checkpoints"`
			}
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				printRawJSON(resp.Result)
				return nil
			}

			if len(result.Checkpoints) == 0 {
				fmt.Println("No checkpoints found.")
				return nil
			}

			tw := newTableWriter()
			fmt.Fprintf(tw, "ID\tLABEL\tCREATED\n")
			for _, cp := range result.Checkpoints {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", cp.ID, cp.Label, cp.Created)
			}
			tw.Flush()
			return nil
		},
	}
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Output as JSON")
	listCmd.Flags().StringVar(&listSandboxID, "sandbox", "", "Target sandbox ID")

	var restoreSandboxID string
	restoreCmd := &cobra.Command{
		Use:   "restore <id>",
		Short: "Restore to a checkpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint := resolveEndpoint(endpointFlag)
			if err := checkServerReachable(endpoint); err != nil {
				return err
			}

			client := newPBClient(endpoint)
			resp, err := client.rpcCall(cmd.Context(), "state.restore", map[string]any{
				"checkpoint_id": args[0],
			}, restoreSandboxID)
			if err != nil {
				return err
			}
			if resp.Error != nil {
				return fmt.Errorf("error: %s", resp.Error.Message)
			}
			fmt.Printf("Restored to checkpoint: %s\n", args[0])
			return nil
		},
	}
	restoreCmd.Flags().StringVar(&restoreSandboxID, "sandbox", "", "Target sandbox ID")

	cmd.AddCommand(createCmd, listCmd, restoreCmd)
	return cmd
}
