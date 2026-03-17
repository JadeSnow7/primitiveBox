package main

import (
	"os"

	"github.com/spf13/cobra"
)

func newCompletionCmd(rootCmd *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for pb.

Usage:
  # Bash
  pb completion bash > /etc/bash_completion.d/pb

  # Zsh
  pb completion zsh > "${fpath[1]}/_pb"

  # Fish
  pb completion fish > ~/.config/fish/completions/pb.fish

  # PowerShell
  pb completion powershell > pb.ps1`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return rootCmd.GenBashCompletion(os.Stdout)
			case "zsh":
				return rootCmd.GenZshCompletion(os.Stdout)
			case "fish":
				return rootCmd.GenFishCompletion(os.Stdout, true)
			case "powershell":
				return rootCmd.GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return cmd.Help()
			}
		},
	}
	return cmd
}
