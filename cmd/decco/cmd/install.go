package cmd

import (
	"context"
	"os"
	"os/exec"

	"github.com/erwinvaneyk/cobras"
	"github.com/spf13/cobra"
)

type InstallOptions struct {
	ResourcePath string
}

func NewInstallCmd() *cobra.Command {
	opts := &InstallOptions{
		ResourcePath: "./manifests_v2",
	}

	cmd := &cobra.Command{
		Use: "install",
		Run: cobras.Run(opts),
	}

	return cmd
}

func (o *InstallOptions) Complete(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *InstallOptions) Validate() error {
	return nil
}

func (o *InstallOptions) Run(ctx context.Context) error {
	cmd := exec.Command("kubectl", "apply", "-k", o.ResourcePath)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	err := cmd.Run()
	if err != nil {
		return err
	}

	return nil
}
