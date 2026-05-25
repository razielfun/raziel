package cli

import "github.com/spf13/cobra"

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List deployments",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := newClient().get("/v0/deployments")
		if err != nil {
			return err
		}
		outputJSON(result)
		return nil
	},
}
