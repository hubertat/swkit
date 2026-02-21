package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
)

const defaultHostKeyPath = ".ssh/swkit_host_key"

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
