package pty

import (
	"strings"
	"testing"
)

// Slice 4 (#81 / threat-model SE-I1): secrets are injected into the spawned
// agent's env IN MEMORY ONLY — never written to an env file or any path on disk —
// and the in-memory copy is zeroized at session end. These are pure helpers so
// they're unit-tested on darwin even though the PTY spawn itself is linux-only.
//
// White-box (package pty) so the test can inspect the spawn env + zeroization.

func TestBuildSpawnEnv_InjectsSecretsIntoProcessEnv(t *testing.T) {
	base := []string{"HOME=/home/user", "PATH=/usr/bin"}
	se := NewSecretEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-secret", "DB_URL": "postgres://x"})
	defer se.Zero()

	env := buildSpawnEnv(base, se)

	if !containsEnv(env, "HOME=/home/user") || !containsEnv(env, "PATH=/usr/bin") {
		t.Fatal("base env must be preserved")
	}
	if !containsEnv(env, "ANTHROPIC_API_KEY=sk-secret") {
		t.Fatal("secret must be present in the spawned process env (in-memory injection)")
	}
	if !containsEnv(env, "DB_URL=postgres://x") {
		t.Fatal("all injected secrets must be present in env")
	}
}

func TestBuildSpawnEnv_DoesNotTouchDisk(t *testing.T) {
	// The whole point of SE-I1: secrets reach the child ONLY through the env slice
	// (memory), never an env-file path. buildSpawnEnv returns a []string and has
	// no filesystem side effects — there is no file path to leak. Guard the
	// contract: the function returns env in-process and writes nothing.
	se := NewSecretEnv(map[string]string{"SECRET": "v"})
	defer se.Zero()
	env := buildSpawnEnv(nil, se)
	if !containsEnv(env, "SECRET=v") {
		t.Fatal("secret must be injected via env slice")
	}
	// A returned env entry is "KEY=VALUE" — never a file path reference.
	for _, e := range env {
		if strings.HasPrefix(e, "ENV_FILE=") || strings.Contains(e, ".env") {
			t.Fatalf("env must not reference an on-disk env file: %q", e)
		}
	}
}

func TestSecretEnv_ZeroizeWipesValues(t *testing.T) {
	se := NewSecretEnv(map[string]string{"K": "topsecret"})
	// Grab the backing bytes so we can prove they were wiped (not just dropped).
	backing := se.backingFor("K")
	if string(backing) != "topsecret" {
		t.Fatalf("expected backing to hold the secret, got %q", backing)
	}
	se.Zero()
	for i, b := range backing {
		if b != 0 {
			t.Fatalf("byte %d not zeroized after Zero(): %d", i, b)
		}
	}
}

func TestSecretEnv_ZeroizeEmptiesTheMap(t *testing.T) {
	se := NewSecretEnv(map[string]string{"A": "1", "B": "2"})
	se.Zero()
	if got := buildSpawnEnv(nil, se); len(got) != 0 {
		t.Fatalf("no secrets must remain after Zero(): %v", got)
	}
}

func TestCoreDumpRlimit_IsZero(t *testing.T) {
	// SE-I1: disable core dumps for the agent process so a crash can't spill
	// secrets from memory to a core file on disk. The limit must be {0,0}.
	rl := coreDumpRlimit()
	if rl.Cur != 0 || rl.Max != 0 {
		t.Fatalf("core dump rlimit must be {0,0}, got {Cur:%d, Max:%d}", rl.Cur, rl.Max)
	}
}

func containsEnv(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}
