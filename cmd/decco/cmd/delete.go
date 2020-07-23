package cmd

import (
	"context"
	"os"
	"os/exec"

	"github.com/erwinvaneyk/cobras"
	"github.com/spf13/cobra"
)

type DeleteOptions struct {
	// TODO specify namespace
	// TODO force
	// TODO purge all spaces too
}

func NewDeleteCmd() *cobra.Command {
	opts := &DeleteOptions{}

	cmd := &cobra.Command{
		Use: "delete",
		Run: cobras.Run(opts),
	}

	return cmd
}

func (o *DeleteOptions) Complete(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *DeleteOptions) Validate() error {
	return nil
}

func (o *DeleteOptions) Run(ctx context.Context) error {
	cmd := exec.Command("kubectl", "delete", "namespace", "decco")
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd.Run()
}
