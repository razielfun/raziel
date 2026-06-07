package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/raziel-ai/raziel/internal/controlplane"
	"github.com/spf13/cobra"
)

// keystoreDir is where the on-box host keypair and the enrolled host id live.
// The private key never leaves this dir (threat-model D-S1).
func keystoreDir() (string, error) {
	if d := os.Getenv("RAZIEL_AGENT_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".raziel"), nil
}

const hostIDFile = "host_id"

func readHostID(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, hostIDFile))
	if err != nil {
		return ""
	}
	return string(b)
}

func writeHostID(dir, id string) error {
	return os.WriteFile(filepath.Join(dir, hostIDFile), []byte(id), 0o600)
}

// controlPlaneURL is the raziel-web base URL the box dials out to.
func controlPlaneURL() string {
	if u := os.Getenv("RAZIEL_CONTROL_PLANE_URL"); u != "" {
		return u
	}
	return "http://localhost:3000"
}

var enrollCmd = &cobra.Command{
	Use:   "enroll <one-time-code>",
	Short: "Enroll this box with the raziel control plane using a one-time code",
	Long: "Generate (or load) the on-box host keypair, then dial out to the control plane " +
		"and redeem a one-time enrollment code, binding this box's public key to a ComputeHost. " +
		"The private key never leaves this box; afterward the box authenticates with the keypair.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := keystoreDir()
		if err != nil {
			return err
		}
		ks, err := controlplane.LoadOrCreateKeystore(dir)
		if err != nil {
			return fmt.Errorf("keystore: %w", err)
		}
		client := controlplane.NewClient(controlPlaneURL(), ks)
		hostID, err := client.Enroll(args[0])
		if err != nil {
			return err
		}
		if err := writeHostID(dir, hostID); err != nil {
			return fmt.Errorf("persist host id: %w", err)
		}
		outputJSON(map[string]string{
			"status":        "enrolled",
			"computeHostId": hostID,
			"publicKey":     ks.PublicKeyAuthorized(),
		})
		return nil
	},
}

var agentHeartbeatInterval time.Duration

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Run raziel-agentd: hold a heartbeat channel to the control plane",
	Long: "Load the on-box keypair and enrolled host id, then emit a keypair-signed " +
		"heartbeat to the control plane on an interval. Requires a prior `raziel enroll`.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := keystoreDir()
		if err != nil {
			return err
		}
		ks, err := controlplane.LoadOrCreateKeystore(dir)
		if err != nil {
			return fmt.Errorf("keystore: %w", err)
		}
		hostID := readHostID(dir)
		if hostID == "" {
			return fmt.Errorf("not enrolled — run `raziel enroll <code>` first")
		}
		client := controlplane.NewClient(controlPlaneURL(), ks)
		client.SetHostID(hostID)

		ticker := time.NewTicker(agentHeartbeatInterval)
		defer ticker.Stop()
		if err := client.Heartbeat(); err != nil {
			return err
		}
		for range ticker.C {
			if err := client.Heartbeat(); err != nil {
				fmt.Fprintf(os.Stderr, "heartbeat failed: %v\n", err)
			}
		}
		return nil
	},
}

func init() {
	agentCmd.Flags().DurationVar(&agentHeartbeatInterval, "interval", 30*time.Second,
		"heartbeat interval")
}
