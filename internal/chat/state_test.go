package chat

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests cover the requirement that matters most for a chat: a person keeps
// one identity across restarts and deploys, without any manual configuration.

func TestSecretPersistsAcrossRestart(t *testing.T) {
	t.Setenv("SESSION_SECRET", "")
	dir := t.TempDir()

	first := NewSessionStore(dir)
	value := first.sign("0123456789abcdef0123456789abcdef")

	// Simulate a restart: same state directory, fresh store.
	second := NewSessionStore(dir)

	if _, ok := second.parse(value); !ok {
		t.Fatal("cookie issued before the restart must still verify after it")
	}

	// And the on-disk key must not be world readable.
	info, err := os.Stat(filepath.Join(dir, secretFile))
	if err != nil {
		t.Fatalf("secret file: %v", err)
	}

	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("secret file mode = %o, want no group/other access", mode)
	}
}

func TestSecretFromEnvWins(t *testing.T) {
	t.Setenv("SESSION_SECRET", "from-env")
	dir := t.TempDir()

	store := NewSessionStore(dir)

	if string(store.secret) != "from-env" {
		t.Fatalf("secret = %q, want the env value", store.secret)
	}

	if _, err := os.Stat(filepath.Join(dir, secretFile)); err == nil {
		t.Fatal("no key file should be written when SESSION_SECRET is set")
	}
}

// Identity must be stable and derived, because nothing can change it any more.
func TestDefaultProfileIsDerivedWithoutStorage(t *testing.T) {
	dir := t.TempDir()
	sessionID := "fedcba9876543210fedcba9876543210"

	a := NewSessionStore(dir).profileFor(sessionID)
	b := NewSessionStore(dir).profileFor(sessionID)

	if a != b {
		t.Fatalf("derived profile is not stable: %+v vs %+v", a, b)
	}

	if a.RoleIndex != roleIndexFor(sessionID) {
		t.Fatalf("role index = %d, want the derived %d", a.RoleIndex, roleIndexFor(sessionID))
	}
}

func TestStateDirectoryDefaultsAndCreates(t *testing.T) {
	t.Setenv(stateDirEnv, "")
	t.Chdir(t.TempDir())

	dir := StateDirectory()

	if dir != defaultStateDir {
		t.Fatalf("dir = %q, want %q", dir, defaultStateDir)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("state dir was not created: %v", err)
	}

	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("state dir mode = %o, want no group/other access", mode)
	}
}
