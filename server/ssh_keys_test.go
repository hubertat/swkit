package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveAuthorizedKeys covers the "keys if present, else open but warn
// loudly" decision table: explicit-but-missing errors, an existing file
// (explicit or default) enables auth, and a missing default starts open.
func TestResolveAuthorizedKeys(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(existing, []byte("ssh-ed25519 AAAA test@example.com\n"), 0600); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	missing := filepath.Join(dir, "does-not-exist")

	tests := []struct {
		name         string
		configured   string
		wantErr      bool
		wantEnabled  bool
		wantExplicit bool
		wantPath     string
	}{
		{
			name:       "explicit path missing fails",
			configured: missing,
			wantErr:    true,
		},
		{
			name:         "explicit path existing enables auth",
			configured:   existing,
			wantEnabled:  true,
			wantExplicit: true,
			wantPath:     existing,
		},
		{
			name:        "unconfigured default missing starts open",
			configured:  "",
			wantEnabled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := ResolveAuthorizedKeys(tt.configured)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decision.Enabled != tt.wantEnabled {
				t.Errorf("Enabled = %v, want %v", decision.Enabled, tt.wantEnabled)
			}
			if decision.Explicit != tt.wantExplicit {
				t.Errorf("Explicit = %v, want %v", decision.Explicit, tt.wantExplicit)
			}
			if tt.wantPath != "" && decision.Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", decision.Path, tt.wantPath)
			}
		})
	}
}

// TestResolveAuthorizedKeys_DefaultMissingUsesDefaultPath verifies that when
// nothing is configured and the default file is absent, the resolved Path
// still names the default location (used by the caller for its warning
// message) even though auth stays disabled.
func TestResolveAuthorizedKeys_DefaultMissingUsesDefaultPath(t *testing.T) {
	// Run from a temp CWD so the real ".ssh/authorized_keys" (if any exists
	// on the dev machine) can't leak into this test.
	dir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	decision, err := ResolveAuthorizedKeys("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Enabled {
		t.Error("expected auth disabled when default authorized_keys is missing")
	}
	if decision.Path != defaultAuthorizedKeysPath {
		t.Errorf("Path = %q, want %q", decision.Path, defaultAuthorizedKeysPath)
	}
}
