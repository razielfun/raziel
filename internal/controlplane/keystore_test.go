package controlplane_test

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/raziel-ai/raziel/internal/controlplane"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slice 1: keypair is generated on-box and the private key never leaves the box.
// Acceptance criterion D-S1: "keypair generated on-box, private key never leaves".

func TestLoadOrCreate_GeneratesKeypairOnBox(t *testing.T) {
	dir := t.TempDir()

	ks, err := controlplane.LoadOrCreateKeystore(dir)
	require.NoError(t, err)

	// A usable public key is exposed.
	pub := ks.PublicKey()
	assert.Len(t, pub, ed25519.PublicKeySize)

	// The OpenSSH authorized-keys form (what the control plane stores) is available
	// and is an ssh-ed25519 line — never the private half.
	authLine := ks.PublicKeyAuthorized()
	assert.Contains(t, authLine, "ssh-ed25519 ")
	assert.NotContains(t, authLine, "PRIVATE")
}

func TestLoadOrCreate_PersistsUnderDirWithPrivateKeyLocked(t *testing.T) {
	dir := t.TempDir()

	_, err := controlplane.LoadOrCreateKeystore(dir)
	require.NoError(t, err)

	// Private key persisted on-box under the keystore dir.
	keyPath := filepath.Join(dir, "host_ed25519")
	info, err := os.Stat(keyPath)
	require.NoError(t, err, "private key must be written to the keystore dir")

	// Private key file is owner-read/write only (0600) — never group/world readable.
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"private key file must be 0600")

	// The public key sits beside it and is non-sensitive (0644 acceptable).
	pubInfo, err := os.Stat(keyPath + ".pub")
	require.NoError(t, err)
	assert.NotZero(t, pubInfo.Size())
}

func TestLoadOrCreate_IsStableAcrossReloads(t *testing.T) {
	dir := t.TempDir()

	ks1, err := controlplane.LoadOrCreateKeystore(dir)
	require.NoError(t, err)
	first := ks1.PublicKeyAuthorized()

	// A second load must reuse the SAME on-box key, not mint a new one
	// (re-minting would orphan the enrolled ComputeHost on the control plane).
	ks2, err := controlplane.LoadOrCreateKeystore(dir)
	require.NoError(t, err)
	assert.Equal(t, first, ks2.PublicKeyAuthorized())
}

func TestSign_ProducesVerifiableSignatureWithoutExposingPrivateKey(t *testing.T) {
	dir := t.TempDir()
	ks, err := controlplane.LoadOrCreateKeystore(dir)
	require.NoError(t, err)

	challenge := []byte("control-plane-issued-nonce-deadbeef")
	sig := ks.Sign(challenge)

	// Anyone holding the PUBLIC key can verify — proving the keystore signs
	// challenges without ever handing out the private key.
	assert.True(t, ed25519.Verify(ks.PublicKey(), challenge, sig),
		"signature must verify against the public key")

	// Tampered challenge must not verify.
	assert.False(t, ed25519.Verify(ks.PublicKey(), []byte("tampered"), sig))
}
