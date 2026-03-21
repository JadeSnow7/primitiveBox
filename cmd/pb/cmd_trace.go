package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newTraceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trace",
		Short: "Inspect execution traces and CVR decision paths",
	}

	var sandboxID string
	var stepID string
	var jsonMode bool
	var limit int

	inspectCmd := &cobra.Command{
		Use:   "inspect",
		Short: "Inspect trace steps",
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint := resolveEndpoint(endpointFlag)
			if err := checkServerReachable(endpoint); err != nil {
				return err
			}

			client := newPBClient(endpoint)

			if stepID != "" && sandboxID != "" {
				// Fetch single step detail
				path := fmt.Sprintf("/api/v1/sandboxes/%s/trace/%s", sandboxID, stepID)
				data, err := client.get(cmd.Context(), path)
				if err != nil {
					return err
				}
				if jsonMode {
					var v any
					if err := json.Unmarshal(data, &v); err != nil {
						return fmt.Errorf("decode trace detail response: %w", err)
					}
					printJSON(v)
					return nil
				}
				return printTraceDetail(data)
			}

			// List trace steps
			path := "/api/v1/trace"
			if sandboxID != "" {
				path = fmt.Sprintf("/api/v1/sandboxes/%s/trace", sandboxID)
			}
			if limit > 0 {
				path += fmt.Sprintf("?limit=%d", limit)
			}

			data, err := client.get(cmd.Context(), path)
			if err != nil {
				return err
			}

			if jsonMode {
				var v any
				if err := json.Unmarshal(data, &v); err != nil {
					return fmt.Errorf("decode trace list response: %w", err)
				}
				printJSON(v)
				return nil
			}

			return printTraceList(data)
		},
	}
	inspectCmd.Flags().StringVar(&sandboxID, "sandbox", "", "Filter by sandbox ID")
	inspectCmd.Flags().StringVar(&stepID, "step", "", "Specific step ID to inspect")
	inspectCmd.Flags().BoolVar(&jsonMode, "json", false, "Output as JSON")
	inspectCmd.Flags().IntVar(&limit, "limit", 20, "Max number of trace steps to list")

	cmd.AddCommand(inspectCmd)
	return cmd
}

func printTraceList(data []byte) error {
	var steps []struct {
		ID            string `json:"id"`
		PrimitiveID   string `json:"primitive_id"`
		LayerAOutcome string `json:"layer_a_outcome"`
		StrategyName  string `json:"strategy_name"`
		RecoveryPath  string `json:"recovery_path"`
		DurationMS    int64  `json:"duration_ms"`
	}

	// Try array directly or wrapped
	if err := json.Unmarshal(data, &steps); err != nil {
		var wrapper struct {
			Steps json.RawMessage `json:"steps"`
		}
		if err2 := json.Unmarshal(data, &wrapper); err2 == nil {
			if err3 := json.Unmarshal(wrapper.Steps, &steps); err3 != nil {
				return fmt.Errorf("decode trace steps: %w", err3)
			}
		} else {
			// Fallback to raw
			var v any
			if err3 := json.Unmarshal(data, &v); err3 != nil {
				return fmt.Errorf("decode trace payload: %w", err3)
			}
			printJSON(v)
			return nil
		}
	}

	if len(steps) == 0 {
		fmt.Println("No trace steps found.")
		return nil
	}

	tw := newTableWriter()
	fmt.Fprintf(tw, "ID\tPRIMITIVE\tLAYER-A\tSTRATEGY\tRECOVERY\tDURATION\n")
	for _, s := range steps {
		dur := fmt.Sprintf("%dms", s.DurationMS)
		layerA := s.LayerAOutcome
		if layerA == "" {
			layerA = "-"
		}
		strategy := s.StrategyName
		if strategy == "" {
			strategy = "-"
		}
		recovery := s.RecoveryPath
		if recovery == "" {
			recovery = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", truncate(s.ID, 12), s.PrimitiveID, layerA, strategy, recovery, dur)
	}
	tw.Flush()
	return nil
}

func printTraceDetail(data []byte) error {
	var step struct {
		ID               string `json:"id"`
		SandboxID        string `json:"sandbox_id"`
		TraceID          string `json:"trace_id"`
		PrimitiveID      string `json:"primitive_id"`
		CheckpointID     string `json:"checkpoint_id"`
		LayerAOutcome    string `json:"layer_a_outcome"`
		StrategyName     string `json:"strategy_name"`
		StrategyOutcome  string `json:"strategy_outcome"`
		RecoveryPath     string `json:"recovery_path"`
		CVRDepthExceeded bool   `json:"cvr_depth_exceeded"`
		DurationMS       int64  `json:"duration_ms"`
		Attempt          int    `json:"attempt"`
		Timestamp        string `json:"timestamp"`
		IntentSnapshot   any    `json:"intent_snapshot"`
	}
	if err := json.Unmarshal(data, &step); err != nil {
		var v any
		if err2 := json.Unmarshal(data, &v); err2 != nil {
			return fmt.Errorf("decode trace detail payload: %w", err2)
		}
		printJSON(v)
		return nil
	}

	fmt.Printf("Step:              %s\n", step.ID)
	fmt.Printf("Trace:             %s\n", step.TraceID)
	fmt.Printf("Sandbox:           %s\n", step.SandboxID)
	fmt.Printf("Primitive:         %s\n", step.PrimitiveID)
	fmt.Printf("Checkpoint:        %s\n", step.CheckpointID)
	fmt.Printf("Attempt:           %d\n", step.Attempt)
	fmt.Printf("Duration:          %dms\n", step.DurationMS)
	fmt.Printf("Timestamp:         %s\n", step.Timestamp)
	fmt.Println("--- CVR Decision ---")
	fmt.Printf("Layer A Outcome:   %s\n", step.LayerAOutcome)
	fmt.Printf("Strategy:          %s\n", step.StrategyName)
	fmt.Printf("Strategy Outcome:  %s\n", step.StrategyOutcome)
	fmt.Printf("Recovery Path:     %s\n", step.RecoveryPath)
	fmt.Printf("CVR Depth Exceeded: %v\n", step.CVRDepthExceeded)
	if step.IntentSnapshot != nil {
		fmt.Println("--- Intent ---")
		printJSON(step.IntentSnapshot)
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
