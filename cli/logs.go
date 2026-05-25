package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs <deployment-id>",
	Short: "Get deployment logs",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		logType, _ := cmd.Flags().GetString("type")
		path := "/v0/deployments/" + args[0] + "/logs"
		if logType != "" {
			path += "?type=" + logType
		}
		result, err := newClient().get(path)
		if err != nil {
			return fmt.Errorf("get logs: %w", err)
		}
		outputJSON(result)
		return nil
	},
}

func init() {
	logsCmd.Flags().String("type", "", "Log type: build, deploy, runtime")
}
