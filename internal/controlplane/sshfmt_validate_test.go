package controlplane_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/raziel-ai/raziel/internal/controlplane"
	"github.com/stretchr/testify/require"
)

// Validates the authorized-keys output against the system ssh-keygen, proving
// the hand-rolled wire format is genuinely OpenSSH-compatible (not just
// self-consistent). Skips if ssh-keygen is unavailable.
func TestPublicKeyAuthorized_ParsesWithSSHKeygen(t *testing.T) {
	bin, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not available")
	}
	dir := t.TempDir()
	ks, err := controlplane.LoadOrCreateKeystore(dir)
	require.NoError(t, err)

	pubPath := filepath.Join(dir, "validate.pub")
	require.NoError(t, os.WriteFile(pubPath, []byte(ks.PublicKeyAuthorized()+"\n"), 0o644))

	out, err := exec.Command(bin, "-l", "-f", pubPath).CombinedOutput()
	require.NoError(t, err, "ssh-keygen failed: %s", out)
	require.Contains(t, strings.ToUpper(string(out)), "ED25519")
}
