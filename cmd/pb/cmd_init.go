package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
	"primitivebox/internal/config"

	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a PrimitiveBox workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath := ".primitivebox.yaml"
			stateDir := ".primitivebox"

			if !force {
				if _, err := os.Stat(configPath); err == nil {
					return fmt.Errorf("%s already exists (use --force to overwrite)", configPath)
				}
			}

			cfg := config.DefaultConfig()
			data, err := yaml.Marshal(cfg)
			if err != nil {
				return err
			}

			if err := os.WriteFile(configPath, data, 0644); err != nil {
				return err
			}

			if err := os.MkdirAll(stateDir, 0755); err != nil {
				return err
			}

			fmt.Printf("Workspace initialized.\n  Config: %s\n  State:  %s/\n", configPath, stateDir)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing config")
	return cmd
}
