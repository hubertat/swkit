package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const defaultHostKeyPath = ".ssh/swkit_host_key"

// defaultAuthorizedKeysPath is the sibling default for the authorized_keys
// file, alongside the default host key path.
const defaultAuthorizedKeysPath = ".ssh/authorized_keys"

// AuthDecision describes the resolved SSH public-key authentication policy.
type AuthDecision struct {
	// Path is the authorized_keys file that was resolved (explicit or
	// default). Only meaningful when Enabled is true.
	Path string
	// Enabled is true when public-key auth should be turned on because the
	// resolved file exists.
	Enabled bool
	// Explicit is true when the path came from configuration rather than
	// the built-in default.
	Explicit bool
}

// ResolveAuthorizedKeys implements the "keys if present, else open but warn
// loudly" policy:
//   - If configuredPath is set and the file exists, auth is enabled.
//   - If configuredPath is set but the file does NOT exist, that's an
//     error: the operator explicitly asked for auth.
//   - If configuredPath is empty and the default file exists, auth is
//     enabled using the default.
//   - If configuredPath is empty and the default file does not exist, auth
//     is disabled (server starts open) and no error is returned; the caller
//     is expected to warn loudly about this.
func ResolveAuthorizedKeys(configuredPath string) (AuthDecision, error) {
	explicit := configuredPath != ""
	path := configuredPath
	if path == "" {
		path = defaultAuthorizedKeysPath
	}

	if _, err := os.Stat(path); err != nil {
		if explicit {
			return AuthDecision{}, errors.Join(err, fmt.Errorf("configured AuthorizedKeysPath %q not found", path))
		}
		return AuthDecision{Path: path, Enabled: false, Explicit: false}, nil
	}

	return AuthDecision{Path: path, Enabled: true, Explicit: explicit}, nil
}

// EnsureHostKey ensures a host key exists at the given path.
// If the path is empty, uses the default path.
// If the key doesn't exist, generates a new ED25519 key.
func EnsureHostKey(path string) (string, error) {
	if path == "" {
		path = defaultHostKeyPath
	}

	// Check if key exists
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	// Create directory if needed
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", errors.Join(err, errors.New("failed to create directory for host key"))
		}
	}

	// Generate new ED25519 key
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", errors.Join(err, errors.New("failed to generate ED25519 key"))
	}

	// Marshal private key to PKCS8
	pkcs8Key, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", errors.Join(err, errors.New("failed to marshal private key"))
	}

	// Encode to PEM
	pemBlock := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8Key,
	}

	// Write to file
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return "", errors.Join(err, errors.New("failed to create host key file"))
	}
	defer f.Close()

	if err := pem.Encode(f, pemBlock); err != nil {
		return "", errors.Join(err, errors.New("failed to write host key"))
	}

	return path, nil
}
