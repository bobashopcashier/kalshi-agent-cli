package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxCredentialBytes = 64 << 10

type Credentials struct {
	KeyID      string
	PrivateKey *rsa.PrivateKey
}

type fileConfig struct {
	KeyID          string `json:"key_id"`
	PrivateKeyFile string `json:"private_key_file"`
}

type Source struct {
	CredentialsFile string
	Env             func(string) string
}

func (s Source) Load() (Credentials, error) {
	getenv := s.Env
	if getenv == nil {
		getenv = os.Getenv
	}
	configPath := s.CredentialsFile
	if configPath == "" {
		configPath = getenv("KALSHI_CREDENTIALS_FILE")
	}
	var keyID, keyPath string
	if configPath != "" {
		data, err := readSecure(configPath)
		if err != nil {
			return Credentials{}, fmt.Errorf("credentials file: %w", err)
		}
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		var cfg fileConfig
		if err := dec.Decode(&cfg); err != nil {
			return Credentials{}, fmt.Errorf("credentials file: invalid JSON: %w", err)
		}
		if err := ensureEOF(dec); err != nil {
			return Credentials{}, fmt.Errorf("credentials file: %w", err)
		}
		keyID, keyPath = cfg.KeyID, cfg.PrivateKeyFile
		if keyPath != "" && !filepath.IsAbs(keyPath) {
			keyPath = filepath.Join(filepath.Dir(configPath), keyPath)
		}
	} else {
		keyID, keyPath = getenv("KALSHI_API_KEY_ID"), getenv("KALSHI_PRIVATE_KEY_FILE")
	}
	if keyID == "" || keyPath == "" {
		return Credentials{}, errors.New("set KALSHI_CREDENTIALS_FILE or both KALSHI_API_KEY_ID and KALSHI_PRIVATE_KEY_FILE")
	}
	if strings.ContainsAny(keyID, "\r\n") {
		return Credentials{}, errors.New("key_id contains forbidden control bytes")
	}
	keyBytes, err := readSecure(keyPath)
	if err != nil {
		return Credentials{}, fmt.Errorf("private key file: %w", err)
	}
	key, err := parsePrivateKey(keyBytes)
	if err != nil {
		return Credentials{}, fmt.Errorf("private key file: %w", err)
	}
	return Credentials{KeyID: keyID, PrivateKey: key}, nil
}

func (c Credentials) Sign(timestamp, method, path string) (string, error) {
	h := sha256.Sum256([]byte(timestamp + method + path))
	sig, err := rsa.SignPSS(rand.Reader, c.PrivateKey, crypto.SHA256, h[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

func readSecure(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("symlinks are not allowed")
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("must be a regular file")
	}
	if before.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("permissions %04o are too broad; require 0600 or stricter", before.Mode().Perm())
	}
	if before.Size() > maxCredentialBytes {
		return nil, errors.New("file exceeds 64 KiB limit")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) {
		return nil, errors.New("file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxCredentialBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCredentialBytes {
		return nil, errors.New("file exceeds 64 KiB limit")
	}
	return data, nil
}

func parsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, rest := pem.Decode(data)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("expected exactly one PEM block")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("private key is not RSA")
		}
		if rsaKey.N.BitLen() < 2048 {
			return nil, errors.New("RSA private key must be at least 2048 bits")
		}
		return rsaKey, rsaKey.Validate()
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("unsupported RSA private key encoding")
	}
	if key.N.BitLen() < 2048 {
		return nil, errors.New("RSA private key must be at least 2048 bits")
	}
	return key, key.Validate()
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err == io.EOF {
		return nil
	}
	return errors.New("must contain exactly one JSON object")
}
