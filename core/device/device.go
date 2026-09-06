// Package device manages HopDrop's persistent cryptographic device identity.
package device

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const identityFormat = 1

type Identity struct {
	ID         string
	privateKey ed25519.PrivateKey
}

type storedIdentity struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Seed    string `json:"seed"`
}

// NewIdentity creates an in-memory identity for tests and one-shot clients.
func NewIdentity(id string) (*Identity, error) {
	if id == "" {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, fmt.Errorf("device: generate id: %w", err)
		}
		id = hex.EncodeToString(raw[:])
	}
	if !validID(id) {
		return nil, errorsForID()
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("device: generate key: %w", err)
	}
	return &Identity{ID: id, privateKey: key}, nil
}

// LoadOrCreateIdentity reads one versioned JSON identity file or atomically
// creates it. Device ID and private key are one unit and can never drift apart.
func LoadOrCreateIdentity(path string) (*Identity, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		var stored storedIdentity
		if err := json.Unmarshal(data, &stored); err != nil {
			return nil, fmt.Errorf("device: decode identity: %w", err)
		}
		seed, seedErr := hex.DecodeString(stored.Seed)
		if stored.Version != identityFormat || !validID(stored.ID) ||
			seedErr != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("device: invalid identity file %s", path)
		}
		return &Identity{ID: stored.ID, privateKey: ed25519.NewKeyFromSeed(seed)}, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("device: read identity: %w", err)
	}

	identity, err := NewIdentity("")
	if err != nil {
		return nil, err
	}
	stored := storedIdentity{
		Version: identityFormat,
		ID:      identity.ID,
		Seed:    hex.EncodeToString(identity.privateKey.Seed()),
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("device: create identity directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".hopdrop-identity-*")
	if err != nil {
		return nil, fmt.Errorf("device: create identity temp file: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("device: protect identity: %w", err)
	}
	if err := json.NewEncoder(temporary).Encode(stored); err != nil {
		return nil, fmt.Errorf("device: write identity: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return nil, fmt.Errorf("device: sync identity: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return nil, fmt.Errorf("device: close identity: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return nil, fmt.Errorf("device: install identity: %w", err)
	}
	removeTemporary = false
	return identity, nil
}

func validID(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 16 && value == strings.ToLower(value)
}

func errorsForID() error {
	return fmt.Errorf("device: id must be 32 lowercase hexadecimal characters")
}

// Fingerprint is the SHA-256 hash of the DER SubjectPublicKeyInfo.
func (i *Identity) Fingerprint() string {
	der, err := x509.MarshalPKIXPublicKey(i.privateKey.Public())
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// TLSCertificate creates a one-year self-signed TLS 1.3 identity. Trust is
// established by public-key pinning, not by a public certificate authority.
func (i *Identity) TLSCertificate(name string) (tls.Certificate, error) {
	serialBytes := make([]byte, 16)
	if _, err := rand.Read(serialBytes); err != nil {
		return tls.Certificate{}, fmt.Errorf("device: certificate serial: %w", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          new(big.Int).SetBytes(serialBytes),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, i.privateKey.Public(), i.privateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("device: create certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("device: parse certificate: %w", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der}, PrivateKey: i.privateKey, Leaf: leaf,
	}, nil
}

func CertificateFingerprint(certificate *x509.Certificate) string {
	if certificate == nil {
		return ""
	}
	sum := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}
