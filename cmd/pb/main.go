package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"primitivebox/internal/audit"
	"primitivebox/internal/config"
	"primitivebox/internal/primitive"
	"primitivebox/internal/rpc"
	"primitivebox/internal/sandbox"

	"github.com/spf13/cobra"
)

var (
	version = "0.1.0-dev"
	cfgFile string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "pb",
		Short: "PrimitiveBox — local and sandboxed JSON-RPC primitives for agents",
		Long: `PrimitiveBox provides a host-side gateway plus workspace primitives so
agents can operate on local projects or Docker sandboxes through JSON-RPC.`,
	}

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: .primitivebox.yaml)")
	rootCmd.AddCommand(
		newVersionCmd(),
		newServerCmd(),
		newSandboxCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the PrimitiveBox version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("PrimitiveBox %s\n", version)
		},
	}
}

func newServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Manage the PrimitiveBox host gateway server",
	}

	var port int
	var host string
	var workspaceDir string

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the host JSON-RPC server and sandbox gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServer(host, port, workspaceDir)
		},
	}

	startCmd.Flags().StringVar(&host, "host", "", "Host interface to bind (defaults to config value)")
	startCmd.Flags().IntVar(&port, "port", 8080, "Port to listen on (0 = auto-assign)")
	startCmd.Flags().StringVar(&workspaceDir, "workspace", ".", "Workspace directory for host primitives")
	cmd.AddCommand(startCmd)
	return cmd
}

func runServer(host string, port int, workspaceDir string) error {
	cfg := mustLoadConfig()
	workspaceDir = resolveWorkspace(workspaceDir)
	log.Printf("[PrimitiveBox] Workspace: %s", workspaceDir)

	var auditor *audit.Logger
	if cfg.Audit.Enabled {
		var err error
		auditor, err = audit.NewLogger(cfg.Audit.LogDir)
		if err != nil {
			log.Printf("[Audit] Warning: cannot initialize audit logger: %v", err)
		} else {
			defer auditor.Close()
		}
	}

	registry := primitive.NewRegistry()
	registry.RegisterDefaults(workspaceDir, primitive.Options{
		AllowedCommands: cfg.Security.AllowedCommands,
		DefaultTimeout:  cfg.Sandbox.Timeout,
	})
	manager := sandbox.NewManager(sandbox.NewDockerDriver())

	server := rpc.NewServer(registry, auditor, manager)

	if host == "" {
		host = cfg.Server.Host
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	if host == "" {
		addr = fmt.Sprintf("localhost:%d", port)
	}
	if port == 0 {
		addr = "localhost:0"
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Println("[PrimitiveBox] Shutting down...")
		_ = server.Shutdown(context.Background())
	}()

	log.Printf("[PrimitiveBox] Starting gateway on %s", addr)
	return server.ListenAndServe(addr)
}

func newSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage Docker-backed PrimitiveBox sandboxes",
	}

	var image, mountDir, user string
	var cpuLimit float64
	var memoryLimit int64

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create and start a new sandbox",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager := sandbox.NewManager(sandbox.NewDockerDriver())
			ctx := context.Background()
			sb, err := manager.Create(ctx, sandbox.SandboxConfig{
				Image:       image,
				MountSource: mountDir,
				CPULimit:    cpuLimit,
				MemoryLimit: memoryLimit,
				User:        user,
			})
			if err != nil {
				return err
			}
			if err := manager.Start(ctx, sb.ID); err != nil {
				return err
			}
			sb, err = manager.Inspect(ctx, sb.ID)
			if err != nil {
				return err
			}
			printSandbox(sb)
			return nil
		},
	}

	createCmd.Flags().StringVar(&image, "image", config.DefaultConfig().Sandbox.Image, "Container image")
	createCmd.Flags().StringVar(&mountDir, "mount", ".", "Host directory to mount")
	createCmd.Flags().StringVar(&user, "user", config.DefaultConfig().Sandbox.User, "User:group to run as")
	createCmd.Flags().Float64Var(&cpuLimit, "cpu", config.DefaultConfig().Sandbox.CPULimit, "CPU limit (cores)")
	createCmd.Flags().Int64Var(&memoryLimit, "memory", config.DefaultConfig().Sandbox.MemoryLimit, "Memory limit (MB)")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all sandboxes",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager := sandbox.NewManager(sandbox.NewDockerDriver())
			sandboxes, err := manager.List(context.Background())
			if err != nil {
				return err
			}
			for _, sb := range sandboxes {
				fmt.Printf("%s\t%s\t%s\t%s\t%s\n", sb.ID, sb.Status, sb.HealthStatus, sb.Config.Image, sb.RPCEndpoint)
			}
			if len(sandboxes) == 0 {
				fmt.Println("No sandboxes found.")
			}
			return nil
		},
	}

	inspectCmd := &cobra.Command{
		Use:   "inspect <id>",
		Short: "Inspect a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manager := sandbox.NewManager(sandbox.NewDockerDriver())
			sb, err := manager.Inspect(context.Background(), args[0])
			if err != nil {
				return err
			}
			printSandbox(sb)
			return nil
		},
	}

	stopCmd := &cobra.Command{
		Use:   "stop <id>",
		Short: "Stop a running sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manager := sandbox.NewManager(sandbox.NewDockerDriver())
			return manager.Stop(context.Background(), args[0])
		},
	}

	destroyCmd := &cobra.Command{
		Use:   "destroy <id>",
		Short: "Destroy a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manager := sandbox.NewManager(sandbox.NewDockerDriver())
			return manager.Destroy(context.Background(), args[0])
		},
	}

	cmd.AddCommand(createCmd, listCmd, inspectCmd, stopCmd, destroyCmd)
	return cmd
}

func mustLoadConfig() *config.Config {
	cfg := config.DefaultConfig()
	if cfgFile == "" {
		return cfg
	}

	loaded, err := config.LoadFromFile(cfgFile)
	if err != nil {
		log.Printf("[Config] Warning: cannot load %s, using defaults: %v", cfgFile, err)
		return cfg
	}
	return loaded
}

func resolveWorkspace(workspaceDir string) string {
	if workspaceDir != "." {
		return workspaceDir
	}
	wd, err := os.Getwd()
	if err != nil {
		log.Fatalf("cannot get working directory: %v", err)
	}
	return wd
}

func printSandbox(sb *sandbox.Sandbox) {
	data, _ := json.MarshalIndent(sb, "", "  ")
	fmt.Println(string(data))
}
