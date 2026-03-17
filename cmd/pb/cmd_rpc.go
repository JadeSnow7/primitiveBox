package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newRPCCmd() *cobra.Command {
	var params string
	var sandboxID string
	var stream bool
	var jsonMode bool

	cmd := &cobra.Command{
		Use:   "rpc <method>",
		Short: "Send a JSON-RPC call to the server",
		Long:  "Generic JSON-RPC entry point. Calls any registered primitive by method name.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint := resolveEndpoint(endpointFlag)
			if err := checkServerReachable(endpoint); err != nil {
				return err
			}

			method := args[0]

			var p any
			if params != "" {
				if err := json.Unmarshal([]byte(params), &p); err != nil {
					return fmt.Errorf("invalid --params JSON: %w", err)
				}
			} else {
				p = map[string]any{}
			}

			if stream {
				client := newStreamClient(endpoint)
				ctx, cancel := context.WithCancel(cmd.Context())
				defer cancel()

				return client.rpcStream(ctx, method, p, sandboxID, func(event, data string) error {
					switch event {
					case "stdout":
						fmt.Print(data)
					case "stderr":
						fmt.Fprint(os.Stderr, data)
					default:
						if jsonMode {
							fmt.Println(data)
						} else {
							fmt.Printf("[%s] %s\n", event, data)
						}
					}
					return nil
				})
			}

			client := newPBClient(endpoint)
			resp, err := client.rpcCall(cmd.Context(), method, p, sandboxID)
			if err != nil {
				return err
			}

			if resp.Error != nil {
				return fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
			}

			if jsonMode {
				printJSON(resp)
			} else {
				printRawJSON(resp.Result)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&params, "params", "", `JSON parameters (default: {})`)
	cmd.Flags().StringVar(&sandboxID, "sandbox", "", "Target sandbox ID")
	cmd.Flags().BoolVar(&stream, "stream", false, "Use SSE streaming")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "Output full JSON-RPC response")
	return cmd
}
