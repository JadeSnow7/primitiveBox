package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newShellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell",
		Short: "Shell execution primitives",
	}

	var sandboxID string
	var stream bool
	var timeout int

	execCmd := &cobra.Command{
		Use:   "exec <command...>",
		Short: "Execute a shell command",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint := resolveEndpoint(endpointFlag)
			if err := checkServerReachable(endpoint); err != nil {
				return err
			}

			command := strings.Join(args, " ")
			params := map[string]any{"command": command}
			if timeout > 0 {
				params["timeout_ms"] = timeout * 1000
			}

			if stream {
				client := newStreamClient(endpoint)
				ctx, cancel := context.WithCancel(cmd.Context())
				defer cancel()

				return client.rpcStream(ctx, "shell.exec", params, sandboxID, func(event, data string) error {
					switch event {
					case "stdout":
						fmt.Print(data)
					case "stderr":
						fmt.Fprint(os.Stderr, data)
					case "completed":
						// Stream finished
					case "error":
						fmt.Fprintf(os.Stderr, "Error: %s\n", data)
					}
					return nil
				})
			}

			client := newPBClient(endpoint)
			resp, err := client.rpcCall(cmd.Context(), "shell.exec", params, sandboxID)
			if err != nil {
				return err
			}
			if resp.Error != nil {
				return fmt.Errorf("error: %s", resp.Error.Message)
			}
			printRawJSON(resp.Result)
			return nil
		},
	}
	execCmd.Flags().StringVar(&sandboxID, "sandbox", "", "Target sandbox ID")
	execCmd.Flags().BoolVar(&stream, "stream", true, "Stream stdout/stderr in real time")
	execCmd.Flags().IntVar(&timeout, "timeout", 0, "Command timeout in seconds")

	cmd.AddCommand(execCmd)
	return cmd
}
