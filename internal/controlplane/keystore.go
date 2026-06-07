// Package controlplane implements raziel-agentd's outbound (dial-out) trust
// model: an on-box ed25519 keystore, one-time enrollment-code redemption, and a
// keypair-authenticated heartbeat channel against the raziel-web control plane.
//
// Trust model (threat-model rule D-S1): the host private key is generated on-box
// and NEVER leaves it. There is deliberately no accessor that returns the private
// key material — the keystore only exposes the public key and a Sign method.
package controlplane

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	privKeyFile = "host_ed25519"
	pubKeyFile  = "host_ed25519.pub"

	// privKeyMode keeps the private key owner-only. Never relax this.
	privKeyMode = 0o600
	pubKeyMode  = 0o644
	dirMode     = 0o700
)

// Keystore holds the on-box host identity. The private key is unexported and has
// no accessor — callers can obtain the public key and sign challenges, nothing more.
type Keystore struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// LoadOrCreateKeystore loads the host keypair from dir, generating and persisting
// a fresh ed25519 keypair on first use. The keypair is stable across reloads so a
// host keeps the same identity (re-minting would orphan its enrolled ComputeHost).
func LoadOrCreateKeystore(dir string) (*Keystore, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("controlplane: create keystore dir: %w", err)
	}
	keyPath := filepath.Join(dir, privKeyFile)

	if seed, err := os.ReadFile(keyPath); err == nil {
		if len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("controlplane: corrupt host key (got %d bytes, want %d)", len(seed), ed25519.SeedSize)
		}
		priv := ed25519.NewKeyFromSeed(seed)
		return fromPrivate(priv), nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("controlplane: read host key: %w", err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("controlplane: generate keypair: %w", err)
	}

	ks := fromPrivate(priv)
	if err := ks.persist(dir); err != nil {
		return nil, err
	}
	return ks, nil
}

func fromPrivate(priv ed25519.PrivateKey) *Keystore {
	return &Keystore{
		priv: priv,
		pub:  priv.Public().(ed25519.PublicKey),
	}
}

// persist writes the private key seed (0600) and the OpenSSH public key (0644).
// We store only the 32-byte seed for the private half so the file is the minimal
// secret; the public key is derived on load.
func (k *Keystore) persist(dir string) error {
	keyPath := filepath.Join(dir, privKeyFile)
	seed := k.priv.Seed()
	if err := os.WriteFile(keyPath, seed, privKeyMode); err != nil {
		return fmt.Errorf("controlplane: write host key: %w", err)
	}
	// Defend against a permissive umask: force 0600 explicitly.
	if err := os.Chmod(keyPath, privKeyMode); err != nil {
		return fmt.Errorf("controlplane: chmod host key: %w", err)
	}

	pubPath := filepath.Join(dir, pubKeyFile)
	if err := os.WriteFile(pubPath, []byte(k.PublicKeyAuthorized()+"\n"), pubKeyMode); err != nil {
		return fmt.Errorf("controlplane: write host pubkey: %w", err)
	}
	return nil
}

// PublicKey returns the raw ed25519 public key.
func (k *Keystore) PublicKey() ed25519.PublicKey {
	return k.pub
}

// PublicKeyAuthorized returns the OpenSSH authorized-keys form
// ("ssh-ed25519 AAAA..."), which is exactly what the control plane persists in
// ComputeHost.publicKey. The wire format (RFC 8709 / PROTOCOL.certkeys) is a
// sequence of length-prefixed strings: the key-type name followed by the raw
// 32-byte public key. We build it with stdlib only.
func (k *Keystore) PublicKeyAuthorized() string {
	const keyType = "ssh-ed25519"
	blob := make([]byte, 0, 4+len(keyType)+4+len(k.pub))
	blob = appendSSHString(blob, []byte(keyType))
	blob = appendSSHString(blob, k.pub)
	return keyType + " " + base64.StdEncoding.EncodeToString(blob)
}

// appendSSHString appends a uint32-big-endian-length-prefixed byte string, the
// SSH wire encoding for strings.
func appendSSHString(dst, s []byte) []byte {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(s)))
	dst = append(dst, lenBuf[:]...)
	return append(dst, s...)
}

// Sign signs msg with the on-box private key. This is the ONLY way the private
// key is used; the key material itself is never returned.
func (k *Keystore) Sign(msg []byte) []byte {
	return ed25519.Sign(k.priv, msg)
}

// ParseAuthorizedKey is the inverse of PublicKeyAuthorized: it decodes an
// "ssh-ed25519 <base64>" line back to the raw ed25519 public key. The control
// plane uses this to recover the enrolled pubkey for signature verification.
func ParseAuthorizedKey(line string) (ed25519.PublicKey, error) {
	const keyType = "ssh-ed25519"
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 2 || fields[0] != keyType {
		return nil, fmt.Errorf("controlplane: not an %s authorized-keys line", keyType)
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return nil, fmt.Errorf("controlplane: decode key blob: %w", err)
	}
	// blob = ssh-string(keyType) ++ ssh-string(pubkey).
	name, rest, err := readSSHString(blob)
	if err != nil || string(name) != keyType {
		return nil, fmt.Errorf("controlplane: bad key blob header")
	}
	pub, _, err := readSSHString(rest)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("controlplane: bad ed25519 public key in blob")
	}
	return ed25519.PublicKey(pub), nil
}

// readSSHString reads one uint32-length-prefixed string off the front of b,
// returning it and the remaining bytes.
func readSSHString(b []byte) (s, rest []byte, err error) {
	if len(b) < 4 {
		return nil, nil, fmt.Errorf("controlplane: truncated ssh string length")
	}
	n := binary.BigEndian.Uint32(b[:4])
	if uint32(len(b)-4) < n {
		return nil, nil, fmt.Errorf("controlplane: truncated ssh string body")
	}
	return b[4 : 4+n], b[4+n:], nil
}
