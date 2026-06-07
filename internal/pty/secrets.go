package pty

import (
	"bytes"
	"syscall"
)

// SecretEnv carries the secrets injected into a spawned agent's environment
// (threat-model SE-I1). Secrets live IN MEMORY ONLY — they are passed to the
// child through its env slice (never an env file or any path on disk) and the
// in-memory copy is zeroized at session end via Zero().
//
// Values are held as []byte (not string) so Zero() can overwrite the backing
// bytes: a Go string is immutable and can't be wiped, but a []byte can. Re-push
// fresh on each attach by building a new SecretEnv (SE-I1 "re-push each attach").
type SecretEnv struct {
	vals map[string][]byte
}

// NewSecretEnv copies the given key/value secrets into a zeroizable carrier.
func NewSecretEnv(secrets map[string]string) *SecretEnv {
	vals := make(map[string][]byte, len(secrets))
	for k, v := range secrets {
		b := make([]byte, len(v))
		copy(b, v)
		vals[k] = b
	}
	return &SecretEnv{vals: vals}
}

// Zero overwrites every secret's backing bytes with zeros and drops the map, so
// no plaintext secret survives in this carrier past session end (SE-I1).
func (s *SecretEnv) Zero() {
	for k, b := range s.vals {
		for i := range b {
			b[i] = 0
		}
		delete(s.vals, k)
	}
}

// backingFor returns the live backing slice for a key (test-only inspection of
// zeroization; unexported so it isn't part of the public surface).
func (s *SecretEnv) backingFor(key string) []byte {
	return s.vals[key]
}

// values returns the injected secret values, to feed the scrollback scrubber
// (SE-I2). Returns the live backing slices — callers must only read them, and
// after Zero() they are empty.
func (s *SecretEnv) values() [][]byte {
	out := make([][]byte, 0, len(s.vals))
	for _, b := range s.vals {
		out = append(out, b)
	}
	return out
}

// redactionMarker replaces a redacted secret value (mirrors lib/scrub-secrets.ts).
var redactionMarker = []byte("[REDACTED]")

// minScrubbableLen mirrors lib/scrub-secrets.ts: a 1-3 byte value appears all
// over normal output, so redacting it would shred the stream for no gain.
const minScrubbableLen = 4

// scrubSecrets returns data with every occurrence of each secret value replaced
// by the redaction marker (SE-I2). Values are matched literally; empty and
// trivially short values are skipped so the scrub can't over-redact. Used on the
// PTY scrollback before it is persisted/returned, so a secret the agent echoed
// never leaves the box in plaintext.
func scrubSecrets(data []byte, secrets [][]byte) []byte {
	out := data
	for _, sec := range secrets {
		if len(bytes.TrimSpace(sec)) < minScrubbableLen {
			continue
		}
		out = bytes.ReplaceAll(out, sec, redactionMarker)
	}
	return out
}

// buildSpawnEnv returns base ++ the secret KEY=VALUE entries, assembled in
// memory. There is no file path in the result: secrets reach the child only
// through this slice (SE-I1 — never written to disk).
func buildSpawnEnv(base []string, secrets *SecretEnv) []string {
	env := make([]string, 0, len(base)+len(secrets.vals))
	env = append(env, base...)
	for k, b := range secrets.vals {
		env = append(env, k+"="+string(b))
	}
	return env
}

// coreDumpRlimit is the RLIMIT_CORE the agent process is spawned with: {0,0}
// disables core dumps entirely, so a crash can't spill secrets from process
// memory to a core file on disk (SE-I1). Applied in the linux spawn path.
func coreDumpRlimit() syscall.Rlimit {
	return syscall.Rlimit{Cur: 0, Max: 0}
}
