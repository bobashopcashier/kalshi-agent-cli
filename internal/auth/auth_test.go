package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestSourceLoadsSecureRelativeKeyAndSigns(t *testing.T) {
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: mustPKCS8(t, key)})
	writeFile(t, filepath.Join(dir, "key.pem"), keyPEM, 0o600)
	writeFile(t, filepath.Join(dir, "credentials.json"), []byte(`{"key_id":"kid","private_key_file":"key.pem"}`), 0o600)
	creds, err := (Source{CredentialsFile: filepath.Join(dir, "credentials.json")}).Load()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := creds.Sign("1700000000000", "GET", "/trade-api/v2/portfolio/balance")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("1700000000000GET/trade-api/v2/portfolio/balance"))
	if err := rsa.VerifyPSS(&key.PublicKey, crypto.SHA256, hash[:], decoded, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}); err != nil {
		t.Fatal(err)
	}
}

func TestSourceRejectsBroadPermissionsSymlinkAndInlineSecret(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "broad.json"), []byte(`{"key_id":"kid","private_key_file":"key.pem"}`), 0o644)
	if _, err := (Source{CredentialsFile: filepath.Join(dir, "broad.json")}).Load(); err == nil {
		t.Fatal("accepted broad permissions")
	}
	writeFile(t, filepath.Join(dir, "real.json"), []byte(`{"key_id":"kid","private_key_file":"key.pem"}`), 0o600)
	if err := os.Symlink(filepath.Join(dir, "real.json"), filepath.Join(dir, "link.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := (Source{CredentialsFile: filepath.Join(dir, "link.json")}).Load(); err == nil {
		t.Fatal("accepted symlink")
	}
	writeFile(t, filepath.Join(dir, "inline.json"), []byte(`{"key_id":"kid","private_key":"SECRET"}`), 0o600)
	if _, err := (Source{CredentialsFile: filepath.Join(dir, "inline.json")}).Load(); err == nil {
		t.Fatal("accepted inline secret field")
	}
}

func mustPKCS8(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
