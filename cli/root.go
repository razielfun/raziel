package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "raziel",
	Short: "Raziel — sandboxed agent deployment platform",
	Long:  "Deploy AI agent-built applications to live HTTPS URLs with full isolation and observability.",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(deployCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(getCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(destroyCmd)
}

// outputJSON prints v as formatted JSON to stdout.
func outputJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v) //nolint:errcheck
}

// fatalf prints an error JSON and exits.
func fatalf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	json.NewEncoder(os.Stderr).Encode(map[string]string{"error": msg}) //nolint:errcheck
	os.Exit(1)
}
