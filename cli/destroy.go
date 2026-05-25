package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var destroyCmd = &cobra.Command{
	Use:   "destroy <deployment-id>",
	Short: "Destroy a deployment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := newClient().delete("/v0/deployments/" + args[0])
		if err != nil {
			return fmt.Errorf("destroy: %w", err)
		}
		outputJSON(result)
		return nil
	},
}
