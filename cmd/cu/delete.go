package main

import (
	"github.com/dagger/container-use/cli"
	"github.com/spf13/cobra"
)

var deleteCmd = &cobra.Command{
	Use:               "delete <env>...",
	Short:             "Delete environments",
	Long:              `Delete one or more environments and their associated resources.`,
	Args:              cobra.MinimumNArgs(1),
	ValidArgsFunction: suggestEnvironments,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Uses shared implementation from cli package
		return cli.DeleteEnvironments(cmd.Context(), ".", args)
	},
}

func init() {
	rootCmd.AddCommand(deleteCmd)
}
