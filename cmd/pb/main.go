package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	pbui "primitivebox/cmd/pb-ui"
	"primitivebox/internal/audit"
	"primitivebox/internal/config"
	"primitivebox/internal/control"
	"primitivebox/internal/eventing"
	"primitivebox/internal/primitive"
	"primitivebox/internal/rpc"
	"primitivebox/internal/sandbox"

	"github.com/spf13/cobra"
)

var (
	version      = "1.0.0"
	cfgFile      string
	endpointFlag string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "pb",
		Short: "PrimitiveBox — local and sandboxed JSON-RPC primitives for agents",
		Long: `PrimitiveBox provides a host-side gateway plus workspace primitives so
agents can operate on local projects or Docker sandboxes through JSON-RPC.`,
	}

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: .primitivebox.yaml)")
	rootCmd.PersistentFlags().StringVar(&endpointFlag, "endpoint", "", "PrimitiveBox server URL (default: http://localhost:8080, env: PB_ENDPOINT)")
	rootCmd.AddCommand(
		newVersionCmd(),
		newServerCmd(),
		newSandboxCmd(),
		newRPCCmd(),
		newPrimitiveCmd(),
		newFSCmd(),
		newShellCmd(),
		newCheckpointCmd(),
		newEventsCmd(),
		newTraceCmd(),
		newInitCmd(),
		newDoctorCmd(),
		newCompletionCmd(rootCmd),
		newPackageCmd(),
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
	var sandboxMode bool
	var serveUI bool

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the host JSON-RPC server and sandbox gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServer(host, port, workspaceDir, sandboxMode, serveUI)
		},
	}

	startCmd.Flags().StringVar(&host, "host", "", "Host interface to bind (defaults to config value)")
	startCmd.Flags().IntVar(&port, "port", 8080, "Port to listen on (0 = auto-assign)")
	startCmd.Flags().StringVar(&workspaceDir, "workspace", ".", "Workspace directory for host primitives")
	startCmd.Flags().BoolVar(&sandboxMode, "sandbox-mode", false, "Run the server as a sandbox-local executor")
	startCmd.Flags().BoolVar(&serveUI, "ui", false, "Serve the embedded inspector UI")
	_ = startCmd.Flags().MarkHidden("sandbox-mode")
	cmd.AddCommand(startCmd)
	return cmd
}

func runServer(host string, port int, workspaceDir string, sandboxMode, serveUI bool) error {
	cfg := mustLoadConfig()
	workspaceDir = resolveWorkspace(workspaceDir)
	normalizeSandboxRuntimePaths(cfg, workspaceDir, sandboxMode)
	log.Printf("[PrimitiveBox] Workspace: %s", workspaceDir)
	if sandboxMode {
		log.Printf("[PrimitiveBox] Sandbox runtime state dir: %s", filepath.Join(workspaceDir, ".primitivebox"))
	}

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
		SandboxMode:     sandboxMode,
	})
	if sandboxMode {
		registry.RegisterSandboxExtras(workspaceDir, primitive.Options{
			AllowedCommands: cfg.Security.AllowedCommands,
			DefaultTimeout:  cfg.Sandbox.Timeout,
			SandboxMode:     true,
		})
	}

	store, err := control.OpenSQLiteStore(cfg.Control.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	bus := eventing.NewBus(store)

	// In sandbox mode the server is the in-container executor; it has no
	// sandbox manager of its own and must accept app.register calls.
	var manager *sandbox.Manager
	if !sandboxMode {
		manager = newManager(cfg, store, bus)
	}

	server := rpc.NewServer(registry, auditor, manager)
	server.RegisterAppRegistry(control.NewSQLiteAppRegistry(store, bus))
	server.AttachEventing(bus, store)
	server.SetAllowedOrigins([]string{"http://localhost:5173"})
	if serveUI {
		uiFS, err := pbui.DistFS()
		if err != nil {
			return err
		}
		server.AttachUI(uiFS)
	}

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
	if manager != nil {
		go manager.RunReaper(ctx, time.Duration(cfg.Control.ReaperIntervalSeconds)*time.Second)
	}

	log.Printf("[PrimitiveBox] Starting gateway on %s", addr)
	serveErr := server.ListenAndServe(addr)
	if serveErr != nil && serveErr == http.ErrServerClosed {
		return nil
	}
	return serveErr
}

func newSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage Docker-backed PrimitiveBox sandboxes",
	}

	var image, mountDir, user, driverName, namespace, networkMode string
	var cpuLimit float64
	var memoryLimit int64
	var ttlSeconds, idleTTLSeconds int64
	var networkHosts, networkCIDRs []string
	var networkPorts []int

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create and start a new sandbox",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := mustLoadConfig()
			store, err := control.OpenSQLiteStore(cfg.Control.DBPath)
			if err != nil {
				return err
			}
			defer store.Close()
			manager := newManager(cfg, store, nil)
			ctx := context.Background()
			mountSpecified := cmd.Flags().Changed("mount")
			if driverName == "kubernetes" && mountSpecified && mountDir != "" {
				return fmt.Errorf("--mount is unsupported for kubernetes sandboxes in v1; workspaces are PVC-backed")
			}
			if driverName == "kubernetes" && !mountSpecified {
				mountDir = ""
			}
			if driverName != "kubernetes" && mountDir == "" {
				mountDir = "."
			}

			sb, err := manager.Create(ctx, sandbox.SandboxConfig{
				Driver:      driverName,
				Image:       image,
				MountSource: mountDir,
				CPULimit:    cpuLimit,
				MemoryLimit: memoryLimit,
				User:        user,
				Namespace:   namespace,
				Lifecycle: sandbox.LifecyclePolicy{
					TTLSeconds:     ttlSeconds,
					IdleTTLSeconds: idleTTLSeconds,
				},
				NetworkPolicy: sandbox.NetworkPolicy{
					Mode:       sandbox.NetworkMode(networkMode),
					AllowHosts: append([]string(nil), networkHosts...),
					AllowCIDRs: append([]string(nil), networkCIDRs...),
					AllowPorts: append([]int(nil), networkPorts...),
				},
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
	createCmd.Flags().StringVar(&driverName, "driver", "docker", "Runtime driver: docker or kubernetes")
	createCmd.Flags().StringVar(&mountDir, "mount", ".", "Host directory to mount")
	createCmd.Flags().StringVar(&user, "user", config.DefaultConfig().Sandbox.User, "User:group to run as")
	createCmd.Flags().Float64Var(&cpuLimit, "cpu", config.DefaultConfig().Sandbox.CPULimit, "CPU limit (cores)")
	createCmd.Flags().Int64Var(&memoryLimit, "memory", config.DefaultConfig().Sandbox.MemoryLimit, "Memory limit (MB)")
	createCmd.Flags().StringVar(&namespace, "namespace", "default", "Runtime namespace / tenancy scope")
	createCmd.Flags().Int64Var(&ttlSeconds, "ttl", 0, "Absolute sandbox TTL in seconds")
	createCmd.Flags().Int64Var(&idleTTLSeconds, "idle-ttl", 0, "Idle sandbox TTL in seconds")
	createCmd.Flags().StringVar(&networkMode, "network-mode", string(sandbox.NetworkModeNone), "Network policy mode: none, full, policy")
	createCmd.Flags().StringSliceVar(&networkHosts, "network-host", nil, "Allowed egress hostname (repeatable)")
	createCmd.Flags().StringSliceVar(&networkCIDRs, "network-cidr", nil, "Allowed egress CIDR (repeatable)")
	createCmd.Flags().IntSliceVar(&networkPorts, "network-port", nil, "Allowed egress port (repeatable)")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all sandboxes",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := mustLoadConfig()
			store, err := control.OpenSQLiteStore(cfg.Control.DBPath)
			if err != nil {
				return err
			}
			defer store.Close()
			manager := newManager(cfg, store, nil)
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
			cfg := mustLoadConfig()
			store, err := control.OpenSQLiteStore(cfg.Control.DBPath)
			if err != nil {
				return err
			}
			defer store.Close()
			manager := newManager(cfg, store, nil)
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
			cfg := mustLoadConfig()
			store, err := control.OpenSQLiteStore(cfg.Control.DBPath)
			if err != nil {
				return err
			}
			defer store.Close()
			manager := newManager(cfg, store, nil)
			return manager.Stop(context.Background(), args[0])
		},
	}

	destroyCmd := &cobra.Command{
		Use:   "destroy <id>",
		Short: "Destroy a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := mustLoadConfig()
			store, err := control.OpenSQLiteStore(cfg.Control.DBPath)
			if err != nil {
				return err
			}
			defer store.Close()
			manager := newManager(cfg, store, nil)
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

func normalizeSandboxRuntimePaths(cfg *config.Config, workspaceDir string, sandboxMode bool) {
	if !sandboxMode {
		return
	}

	stateDir := filepath.Join(workspaceDir, ".primitivebox")
	cfg.Control.DBPath = filepath.Join(stateDir, "controlplane.db")
	cfg.Audit.LogDir = filepath.Join(stateDir, "audit")
}

func printSandbox(sb *sandbox.Sandbox) {
	data, _ := json.MarshalIndent(sb, "", "  ")
	fmt.Println(string(data))
}

func newManager(cfg *config.Config, store sandbox.Store, bus *eventing.Bus) *sandbox.Manager {
	var kubeClient sandbox.KubernetesClient
	if client, err := sandbox.NewDefaultKubernetesClient(); err == nil {
		kubeClient = client
	} else {
		log.Printf("[Kubernetes] Warning: cannot initialize kubernetes client: %v", err)
	}

	kubeDriver := sandbox.NewKubernetesDriver(kubeClient).WithSandboxLookup(func(ctx context.Context, sandboxID string) (*sandbox.Sandbox, bool, error) {
		return store.Get(ctx, sandboxID)
	})
	router := sandbox.NewRouterDriver(func(sandboxID string) (string, bool) {
		sb, ok, err := store.Get(context.Background(), sandboxID)
		if err != nil || !ok {
			return "", false
		}
		return sb.Driver, true
	},
		sandbox.NewDockerDriver(),
		kubeDriver,
	)

	return sandbox.NewManagerWithOptions(router, sandbox.ManagerOptions{
		Store:       store,
		EventBus:    bus,
		RegistryDir: os.Getenv("PB_SANDBOX_REGISTRY_DIR"),
	})
}
