package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"primitivebox/internal/control"
	"primitivebox/internal/pkgmgr"

	"github.com/spf13/cobra"
)

// isLocalPath reports whether arg should be treated as a local directory path
// rather than a registry package name.
func isLocalPath(arg string) bool {
	return strings.HasPrefix(arg, "./") ||
		strings.HasPrefix(arg, "../") ||
		strings.HasPrefix(arg, "/") ||
		strings.HasPrefix(arg, "~") ||
		arg == "." ||
		arg == ".."
}

func newPackageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "package",
		Aliases: []string{"pkg"},
		Short:   "Manage PrimitiveBox adapter packages",
	}

	cmd.AddCommand(
		newInstallCmd(),
		newRemoveCmd(),
		newPackageListCmd(),
		newSearchCmd(),
		newInfoCmd(),
	)
	return cmd
}

func newInstallCmd() *cobra.Command {
	var mcpCmd string
	var workspaceDir string

	cmd := &cobra.Command{
		Use:   "install <name-or-path>",
		Short: "Install an adapter package (registry name or local Boxfile path)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := args[0]
			endpoint := resolveEndpoint(endpointFlag)
			if err := checkServerReachable(endpoint); err != nil {
				return err
			}

			cfg := mustLoadConfig()
			store, err := control.OpenSQLiteStore(cfg.Control.DBPath)
			if err != nil {
				return err
			}
			defer store.Close()

			pkgStore, err := pkgmgr.NewSQLitePackageStore(store.DB())
			if err != nil {
				return fmt.Errorf("open package store: %w", err)
			}

			workspace := workspaceDir
			if workspace == "" {
				workspace = resolveWorkspace(".")
			}
			pbDir, err := resolvePBDir()
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			var extraArgs []string
			if mcpCmd != "" {
				extraArgs = append(extraArgs, "--cmd", mcpCmd)
			}

			appReg := control.NewSQLiteAppRegistry(store, nil)
			installer := pkgmgr.NewInstaller(pkgStore, pkgmgr.NewLocalRegistry(), appReg, nil, endpoint, workspace, pbDir)

			if isLocalPath(arg) {
				return installFromLocalPath(ctx, arg, installer, extraArgs)
			}

			// Registry-name install
			reg := pkgmgr.NewLocalRegistry()
			manifest, err := reg.Lookup(arg)
			if err != nil {
				return fmt.Errorf("package %q not found in registry", arg)
			}

			binaryPath := resolvePkgTemplate(manifest.Adapter.BinaryPath, pbDir, workspace)
			socketPath := resolvePkgTemplate(manifest.Adapter.SocketPath, pbDir, workspace)

			fmt.Printf("Resolving '%s'...\n", arg)
			fmt.Printf("  adapter    : %s (%s)\n", filepath.Base(binaryPath), manifest.Adapter.Type)
			fmt.Printf("  socket     : %s\n", socketPath)
			if len(manifest.Primitives) > 0 {
				fmt.Printf("  primitives declared: %d\n", len(manifest.Primitives))
			} else {
				fmt.Printf("  primitives declared: dynamic\n")
			}
			fmt.Println()

			if _, err := os.Stat(binaryPath); err != nil {
				return fmt.Errorf("binary not found at %s\nBuild it first with: make build", binaryPath)
			}
			fmt.Printf("  binary found at %s\n", binaryPath)
			fmt.Println("Installing...")

			if err := installer.Install(ctx, arg, extraArgs); err != nil {
				return err
			}

			fmt.Printf("\nPackage '%s' v%s installed.\n", arg, manifest.Version)
			fmt.Println("Run 'pb primitives list' to see the new primitives.")
			return nil
		},
	}

	cmd.Flags().StringVar(&mcpCmd, "mcp-cmd", "", "MCP stdio command to wrap (mcp-bridge only)")
	cmd.Flags().StringVar(&workspaceDir, "workspace", "", "Workspace directory (default: current directory)")
	return cmd
}

func newRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an installed adapter package",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			cfg := mustLoadConfig()
			store, err := control.OpenSQLiteStore(cfg.Control.DBPath)
			if err != nil {
				return err
			}
			defer store.Close()

			pkgStore, err := pkgmgr.NewSQLitePackageStore(store.DB())
			if err != nil {
				return fmt.Errorf("open package store: %w", err)
			}

			workspace := resolveWorkspace(".")
			pbDir, err := resolvePBDir()
			if err != nil {
				return err
			}

			reg := pkgmgr.NewLocalRegistry()
			appReg := control.NewSQLiteAppRegistry(store, nil)
			installer := pkgmgr.NewInstaller(pkgStore, reg, appReg, nil, "", workspace, pbDir)

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if err := installer.Remove(ctx, name); err != nil {
				return err
			}

			fmt.Printf("Package '%s' removed.\n", name)
			return nil
		},
	}
}

func newPackageListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed adapter packages",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := mustLoadConfig()
			store, err := control.OpenSQLiteStore(cfg.Control.DBPath)
			if err != nil {
				return err
			}
			defer store.Close()

			pkgStore, err := pkgmgr.NewSQLitePackageStore(store.DB())
			if err != nil {
				return fmt.Errorf("open package store: %w", err)
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			pkgs, err := pkgStore.List(ctx)
			if err != nil {
				return err
			}

			if len(pkgs) == 0 {
				fmt.Println("No packages installed.")
				return nil
			}

			tw := newTableWriter()
			fmt.Fprintf(tw, "NAME\tVERSION\tSTATUS\tINSTALLED AT\n")
			for _, pkg := range pkgs {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
					pkg.Name,
					pkg.Version,
					pkg.Status,
					pkg.InstalledAt.Format("2006-01-02 15:04:05"),
				)
			}
			tw.Flush()
			return nil
		},
	}
}

func newSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "search [query]",
		Short: "Search available adapter packages",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg := pkgmgr.NewLocalRegistry()
			pkgs := reg.List()

			query := ""
			if len(args) > 0 {
				query = strings.ToLower(args[0])
			}

			// Sort by name for deterministic output
			sort.Slice(pkgs, func(i, j int) bool {
				return pkgs[i].Name < pkgs[j].Name
			})

			tw := newTableWriter()
			fmt.Fprintf(tw, "NAME\tVERSION\tDESCRIPTION\n")
			for _, pkg := range pkgs {
				if query != "" {
					if !strings.Contains(strings.ToLower(pkg.Name), query) &&
						!strings.Contains(strings.ToLower(pkg.Description), query) {
						continue
					}
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", pkg.Name, pkg.Version, pkg.Description)
			}
			tw.Flush()
			return nil
		},
	}
}

func newInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <name>",
		Short: "Show details about a package",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			reg := pkgmgr.NewLocalRegistry()

			pkg, err := reg.Lookup(name)
			if err != nil {
				return fmt.Errorf("package %q not found", name)
			}

			fmt.Printf("Name:        %s\n", pkg.Name)
			fmt.Printf("Version:     %s\n", pkg.Version)
			fmt.Printf("Description: %s\n", pkg.Description)
			fmt.Printf("Adapter:     %s (%s)\n", pkg.Adapter.BinaryPath, pkg.Adapter.Type)
			fmt.Printf("Socket:      %s\n", pkg.Adapter.SocketPath)
			if pkg.Healthcheck != nil {
				fmt.Printf("Healthcheck: %s (timeout: %s)\n", pkg.Healthcheck.Primitive, pkg.Healthcheck.Timeout)
			}
			if len(pkg.Primitives) > 0 {
				fmt.Printf("\nPrimitives (%d):\n", len(pkg.Primitives))
				tw := newTableWriter()
				fmt.Fprintf(tw, "  NAME\tCATEGORY\tSIDE EFFECT\tRISK\tREVERSIBLE\n")
				for _, p := range pkg.Primitives {
					rev := "no"
					if p.Intent.Reversible {
						rev = "yes"
					}
					fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n",
						p.Name, p.Intent.Category, p.Intent.SideEffect, p.Intent.RiskLevel, rev)
				}
				tw.Flush()
			} else {
				fmt.Println("\nPrimitives: dynamic (registered at runtime)")
			}
			return nil
		},
	}
}

// installFromLocalPath loads a Boxfile from the given directory, validates it,
// and installs the resulting manifest. No scripts are executed; the Boxfile is
// parsed as pure declarative metadata.
func installFromLocalPath(ctx context.Context, dir string, installer *pkgmgr.Installer, extraArgs []string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", dir, err)
	}

	bf, boxfilePath, err := pkgmgr.LoadBoxfile(absDir)
	if err != nil {
		return fmt.Errorf("load Boxfile from %s: %w", absDir, err)
	}
	fmt.Printf("Found Boxfile at %s\n", boxfilePath)

	if err := pkgmgr.ValidateBoxfile(bf); err != nil {
		return fmt.Errorf("validate Boxfile: %w", err)
	}

	manifest, err := pkgmgr.BoxfileToManifest(bf, absDir)
	if err != nil {
		return fmt.Errorf("parse Boxfile manifest: %w", err)
	}

	fmt.Printf("Resolving '%s' v%s...\n", manifest.Name, manifest.Version)
	fmt.Printf("  adapter    : %s (%s)\n", filepath.Base(manifest.Adapter.BinaryPath), manifest.Adapter.Type)
	fmt.Printf("  socket     : %s\n", manifest.Adapter.SocketPath)
	if len(manifest.Primitives) > 0 {
		fmt.Printf("  primitives declared: %d\n", len(manifest.Primitives))
	} else {
		fmt.Printf("  primitives declared: dynamic\n")
	}
	if bf.Bootstrap != nil && len(bf.Bootstrap.Files) > 0 {
		fmt.Printf("  bootstrap files: %d\n", len(bf.Bootstrap.Files))
	}
	fmt.Println()
	fmt.Println("Installing...")

	if err := installer.InstallFromManifest(ctx, manifest, extraArgs); err != nil {
		return err
	}

	fmt.Printf("\nPackage '%s' v%s installed.\n", manifest.Name, manifest.Version)
	fmt.Println("Run 'pb primitives list' to see the new primitives.")
	return nil
}

// resolvePBDir returns the directory containing the pb binary.
func resolvePBDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve pb binary path: %w", err)
	}
	return filepath.Dir(exe), nil
}

// resolvePkgTemplate substitutes {pb_dir} and {workspace} in a template string.
func resolvePkgTemplate(s, pbDir, workspace string) string {
	s = strings.ReplaceAll(s, "{pb_dir}", pbDir)
	s = strings.ReplaceAll(s, "{workspace}", workspace)
	return s
}
