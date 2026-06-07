package pty

import (
	"bytes"
	"testing"
)

// Slice 5 (#81 / threat-model SE-I2): a known secret echoed into the PTY
// scrollback must be redacted before that buffer is persisted or returned.
// scrubSecrets is a pure value-based redactor (literal match, length-guarded),
// the Go twin of lib/scrub-secrets.ts — unit-tested on darwin.

var marker = []byte("[REDACTED]")

func TestScrubSecrets_RedactsAKnownSecret(t *testing.T) {
	out := scrubSecrets([]byte("export KEY=sk-ant-abc123\n"), [][]byte{[]byte("sk-ant-abc123")})
	if !bytes.Equal(out, []byte("export KEY=[REDACTED]\n")) {
		t.Fatalf("got %q", out)
	}
}

func TestScrubSecrets_RedactsEveryOccurrence(t *testing.T) {
	out := scrubSecrets([]byte("hunter2 then hunter2"), [][]byte{[]byte("hunter2")})
	if bytes.Contains(out, []byte("hunter2")) {
		t.Fatalf("secret survived: %q", out)
	}
	if got, want := out, []byte("[REDACTED] then [REDACTED]"); !bytes.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestScrubSecrets_LeavesCleanTextUntouched(t *testing.T) {
	in := []byte("compiled in 4.2s")
	if out := scrubSecrets(in, [][]byte{[]byte("sk-ant-abc123")}); !bytes.Equal(out, in) {
		t.Fatalf("clean text changed: %q", out)
	}
}

func TestScrubSecrets_SkipsShortValues(t *testing.T) {
	in := []byte("a aa aaa")
	if out := scrubSecrets(in, [][]byte{[]byte("a")}); !bytes.Equal(out, in) {
		t.Fatalf("short value over-redacted: %q", out)
	}
}

func TestScrubSecrets_NoSecretsIsNoOp(t *testing.T) {
	in := []byte("nothing here")
	if out := scrubSecrets(in, nil); !bytes.Equal(out, in) {
		t.Fatalf("no-op failed: %q", out)
	}
}

// Scrollback returned for persistence is scrubbed against the session's injected
// secrets, so a secret the agent echoed never leaves the box in plaintext.
func TestSecretEnv_Values_FeedsTheScrubber(t *testing.T) {
	se := NewSecretEnv(map[string]string{"K": "sk-ant-abc123"})
	defer se.Zero()
	out := scrubSecrets([]byte("leaked sk-ant-abc123"), se.values())
	if bytes.Contains(out, []byte("sk-ant-abc123")) {
		t.Fatalf("session secrets must feed the scrubber: %q", out)
	}
}
