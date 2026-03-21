package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

func newEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Event stream operations",
	}

	var sandboxID string
	var typeFilter string
	var jsonMode bool

	watchCmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch live event stream (SSE)",
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint := resolveEndpoint(endpointFlag)
			if err := checkServerReachable(endpoint); err != nil {
				return err
			}

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			client := newStreamClient(endpoint)
			return client.sseStream(ctx, "/api/v1/events/stream", func(event, data string) error {
				var ev struct {
					SandboxID string `json:"sandbox_id"`
					Type      string `json:"type"`
					Method    string `json:"method"`
					Message   string `json:"message"`
					Timestamp string `json:"timestamp"`
				}
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					if jsonMode {
						fmt.Println(data)
					} else {
						fmt.Fprintln(os.Stdout, data)
					}
					return nil
				}

				// Apply filters
				if sandboxID != "" && ev.SandboxID != sandboxID {
					return nil
				}
				if typeFilter != "" && !strings.HasPrefix(ev.Type, typeFilter) {
					return nil
				}

				if jsonMode {
					fmt.Println(data)
					return nil
				}

				ts := ev.Timestamp
				if len(ts) > 19 {
					ts = ts[:19]
				}
				method := ev.Method
				if method == "" {
					method = "-"
				}
				msg := ev.Message
				if msg == "" {
					msg = ev.Type
				}
				fmt.Fprintf(os.Stdout, "[%s] [%s] [%s] %s\n", ts, ev.Type, method, msg)
				return nil
			})
		},
	}
	watchCmd.Flags().StringVar(&sandboxID, "sandbox", "", "Filter by sandbox ID")
	watchCmd.Flags().StringVar(&typeFilter, "type", "", "Filter by event type prefix (e.g. rpc, sandbox)")
	watchCmd.Flags().BoolVar(&jsonMode, "json", false, "Output raw JSON per event")

	cmd.AddCommand(watchCmd)
	return cmd
}
