package gosmee

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/nacl/box"
)

const encryptedEnvelopeVersion = 1

type encryptedEnvelope struct {
	Encrypted  bool   `json:"encrypted"`
	Version    int    `json:"version"`
	Ephemeral  string `json:"epk"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type storedKeyPair struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

func GenerateKeyPair() (*[32]byte, *[32]byte, error) {
	publicKey, privateKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	return publicKey, privateKey, nil
}

func SaveKeyPair(path string, publicKey *[32]byte, privateKey *[32]byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	stored := storedKeyPair{
		PublicKey:  base64.StdEncoding.EncodeToString(publicKey[:]),
		PrivateKey: base64.StdEncoding.EncodeToString(privateKey[:]),
	}
	encoded, err := json.Marshal(stored) //nolint:gosec // intentionally marshaling key pair for storage
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(encoded); err != nil {
		return err
	}
	return nil
}

func LoadKeyPair(path string) (*[32]byte, *[32]byte, error) {
	if path == "" {
		return nil, nil, fmt.Errorf("key file path is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	return loadKeyPair(data)
}

func EncodePublicKey(key *[32]byte) string {
	return base64.RawURLEncoding.EncodeToString(key[:])
}

func ParsePublicKey(encoded string) (*[32]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}

	return bytesToKey(decoded)
}

func Encrypt(plaintext []byte, recipientPubKey *[32]byte) ([]byte, error) {
	ephemeralPubKey, ephemeralPrivKey, err := GenerateKeyPair()
	if err != nil {
		return nil, err
	}

	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}

	ciphertext := box.Seal(nil, plaintext, &nonce, recipientPubKey, ephemeralPrivKey)
	envelope := encryptedEnvelope{
		Encrypted:  true,
		Version:    encryptedEnvelopeVersion,
		Ephemeral:  base64.StdEncoding.EncodeToString(ephemeralPubKey[:]),
		Nonce:      base64.StdEncoding.EncodeToString(nonce[:]),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}

	return json.Marshal(envelope)
}

func Decrypt(envelope []byte, privateKey *[32]byte) ([]byte, error) {
	parsed, ephemeralPubKey, nonce, ciphertext, err := parseEncryptedEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	if !parsed.Encrypted {
		return nil, fmt.Errorf("payload is not encrypted")
	}

	plaintext, ok := box.Open(nil, ciphertext, nonce, ephemeralPubKey, privateKey)
	if !ok {
		return nil, fmt.Errorf("decrypt payload: invalid ciphertext")
	}

	return plaintext, nil
}

func IsEncrypted(data []byte) bool {
	var e struct {
		Encrypted  bool   `json:"encrypted"`
		Ephemeral  string `json:"epk"`
		Nonce      string `json:"nonce"`
		Ciphertext string `json:"ciphertext"`
	}
	return json.Unmarshal(data, &e) == nil && e.Encrypted && e.Ephemeral != "" && e.Nonce != "" && e.Ciphertext != ""
}

func loadKeyPair(data []byte) (*[32]byte, *[32]byte, error) {
	var stored storedKeyPair
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, nil, fmt.Errorf("unmarshal key file: %w", err)
	}

	publicKey, err := decodeStdKey(stored.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("decode public key: %w", err)
	}
	privateKey, err := decodeStdKey(stored.PrivateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("decode private key: %w", err)
	}

	return publicKey, privateKey, nil
}

func decodeStdKey(encoded string) (*[32]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}

	return bytesToKey(decoded)
}

func bytesToKey(decoded []byte) (*[32]byte, error) {
	if len(decoded) != 32 {
		return nil, fmt.Errorf("invalid key length %d", len(decoded))
	}

	var key [32]byte
	copy(key[:], decoded)
	return &key, nil
}

func parseEncryptedEnvelope(data []byte) (encryptedEnvelope, *[32]byte, *[24]byte, []byte, error) {
	var envelope encryptedEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return encryptedEnvelope{}, nil, nil, nil, err
	}
	if !envelope.Encrypted {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("missing encrypted marker")
	}
	if envelope.Version != encryptedEnvelopeVersion {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("unsupported envelope version %d", envelope.Version)
	}
	if envelope.Ephemeral == "" || envelope.Nonce == "" || envelope.Ciphertext == "" {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("incomplete encrypted envelope")
	}

	ephemeralBytes, err := base64.StdEncoding.DecodeString(envelope.Ephemeral)
	if err != nil {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("decode ephemeral key: %w", err)
	}
	ephemeralKey, err := bytesToKey(ephemeralBytes)
	if err != nil {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("decode ephemeral key: %w", err)
	}

	nonceBytes, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("decode nonce: %w", err)
	}
	if len(nonceBytes) != 24 {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("invalid nonce length %d", len(nonceBytes))
	}
	var nonce [24]byte
	copy(nonce[:], nonceBytes)

	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("decode ciphertext: %w", err)
	}

	return envelope, ephemeralKey, &nonce, ciphertext, nil
}
