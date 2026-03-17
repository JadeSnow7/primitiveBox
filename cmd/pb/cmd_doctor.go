package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check environment health and dependencies",
		RunE: func(cmd *cobra.Command, args []string) error {
			ok := true

			// Go
			if out, err := exec.Command("go", "version").Output(); err == nil {
				fmt.Printf("  [OK] Go: %s\n", trimNL(string(out)))
			} else {
				fmt.Println("  [--] Go: not found (optional, needed for development)")
			}

			// Python
			if out, err := exec.Command("python3", "--version").Output(); err == nil {
				fmt.Printf("  [OK] Python: %s\n", trimNL(string(out)))
			} else {
				fmt.Println("  [--] Python: not found (optional, needed for SDK)")
			}

			// Docker
			if err := exec.Command("docker", "info").Run(); err == nil {
				fmt.Println("  [OK] Docker: available")
			} else {
				fmt.Println("  [!!] Docker: not available (required for sandboxes)")
				ok = false
			}

			// Git
			if out, err := exec.Command("git", "--version").Output(); err == nil {
				fmt.Printf("  [OK] Git: %s\n", trimNL(string(out)))
			} else {
				fmt.Println("  [!!] Git: not found (required for checkpoints)")
				ok = false
			}

			// Config file
			configPath := ".primitivebox.yaml"
			if cfgFile != "" {
				configPath = cfgFile
			}
			if _, err := os.Stat(configPath); err == nil {
				fmt.Printf("  [OK] Config: %s\n", configPath)
			} else {
				fmt.Printf("  [--] Config: %s not found (using defaults, run 'pb init' to create)\n", configPath)
			}

			// Server
			endpoint := resolveEndpoint(endpointFlag)
			client := &http.Client{Timeout: 3 * time.Second}
			if resp, err := client.Get(endpoint + "/health"); err == nil {
				resp.Body.Close()
				fmt.Printf("  [OK] Server: running at %s\n", endpoint)

				// Primitives count
				if primResp, err := client.Get(endpoint + "/primitives"); err == nil {
					defer primResp.Body.Close()
					fmt.Printf("  [OK] Primitives: endpoint reachable\n")
				}
			} else {
				fmt.Printf("  [--] Server: not running at %s\n", endpoint)
			}

			if !ok {
				fmt.Println("\nSome required dependencies are missing.")
				return fmt.Errorf("doctor check failed")
			}
			fmt.Println("\nAll checks passed.")
			return nil
		},
	}
}

func trimNL(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\n' {
		return s[:len(s)-1]
	}
	return s
}
