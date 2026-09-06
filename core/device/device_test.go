package device

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityPersistsAsOneProtectedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")
	first, err := LoadOrCreateIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Fingerprint() != second.Fingerprint() {
		t.Fatal("identity changed after reload")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("identity permissions are too broad: %o", info.Mode().Perm())
	}
	if _, err := os.Stat(path + ".ed25519"); !os.IsNotExist(err) {
		t.Fatal("identity was unexpectedly split across files")
	}
}
