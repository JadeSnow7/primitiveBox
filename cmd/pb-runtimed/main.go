package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"primitivebox/internal/control"
	"primitivebox/internal/eventing"
	"primitivebox/internal/primitive"
	"primitivebox/internal/rpc"
	pbruntime "primitivebox/internal/runtime"
)

func main() {
	var host string
	var port int
	var workspace string
	var appsDir string
	var logDir string
	var dataDir string
	var sandboxID string

	flag.StringVar(&host, "host", "0.0.0.0", "Host interface to bind")
	flag.IntVar(&port, "port", 8080, "Port to listen on")
	flag.StringVar(&workspace, "workspace", "/workspace", "Workspace directory")
	flag.StringVar(&appsDir, "apps-dir", "/apps", "Adapter manifest directory")
	flag.StringVar(&logDir, "log-dir", "/var/log/primitivebox", "Runtime log directory")
	flag.StringVar(&dataDir, "data-dir", "/var/lib/primitivebox", "Runtime data directory")
	flag.StringVar(&sandboxID, "sandbox-id", "", "Sandbox identifier for trace metadata")
	flag.Parse()

	if sandboxID == "" {
		sandboxID = os.Getenv("PRIMITIVEBOX_SANDBOX_ID")
	}
	if sandboxID == "" {
		sandboxID = "sandbox-local"
	}

	rt, err := pbruntime.New(pbruntime.Config{
		WorkspaceDir: workspace,
		AppsDir:      appsDir,
		LogDir:       logDir,
		DataDir:      dataDir,
		SandboxID:    sandboxID,
		Options: primitive.Options{
			SandboxMode:    true,
			DefaultTimeout: 120,
		},
	})
	if err != nil {
		log.Fatalf("start runtime: %v", err)
	}
	defer rt.Close()

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}
	store, err := control.OpenSQLiteStore(filepath.Join(dataDir, "control.db"))
	if err != nil {
		log.Fatalf("open control store: %v", err)
	}
	defer store.Close()
	bus := eventing.NewBus(store)

	server := rpc.NewServer(rt.Registry(), nil, nil)
	server.RegisterAppRegistry(control.NewSQLiteAppRegistry(store, bus))
	server.AttachEventing(bus, store)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	addr := fmt.Sprintf("%s:%d", host, port)
	log.Printf("[pb-runtimed] workspace=%s apps=%s logs=%s", filepath.Clean(workspace), filepath.Clean(appsDir), filepath.Clean(logDir))
	if err := server.ListenAndServe(addr); err != nil && err != http.ErrServerClosed {
		log.Fatalf("runtime server failed: %v", err)
	}
}
