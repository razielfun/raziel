package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var getCmd = &cobra.Command{
	Use:   "get <deployment-id>",
	Short: "Get a deployment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := newClient().get("/v0/deployments/" + args[0])
		if err != nil {
			return fmt.Errorf("get deployment: %w", err)
		}
		outputJSON(result)
		return nil
	},
}
