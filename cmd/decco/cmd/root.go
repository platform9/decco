package cmd

import (
	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands

func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decco",
		Short: "A brief description of your application",
	}

	cmd.AddCommand(
		NewInstallCmd(),
		NewDeleteCmd(),
	)

	return cmd
}
