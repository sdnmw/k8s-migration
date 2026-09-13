package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const envelopeVersion = 1

type Keyring struct {
	activeVersion int
	keys          map[int][]byte
}

type encryptedEnvelope struct {
	Version          int    `json:"version"`
	KeyVersion       int    `json:"keyVersion"`
	WrappedKeyNonce  string `json:"wrappedKeyNonce"`
	WrappedKey       string `json:"wrappedKey"`
	PayloadNonce     string `json:"payloadNonce"`
	EncryptedPayload string `json:"encryptedPayload"`
}

func NewKeyring(activeVersion int, keys map[int][]byte) (*Keyring, error) {
	if activeVersion < 1 {
		return nil, errors.New("active key version must be positive")
	}
	copyKeys := make(map[int][]byte, len(keys))
	for version, key := range keys {
		if len(key) != 32 {
			return nil, fmt.Errorf("master key version %d must contain 32 bytes", version)
		}
		copyKeys[version] = append([]byte(nil), key...)
	}
	if _, ok := copyKeys[activeVersion]; !ok {
		return nil, errors.New("active master key is missing")
	}
	return &Keyring{activeVersion: activeVersion, keys: copyKeys}, nil
}

func LoadKeyringFile(path string, activeVersion int) (*Keyring, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read credential master key: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, errors.New("credential master key must be base64 encoded")
	}
	return NewKeyring(activeVersion, map[int][]byte{activeVersion: key})
}

func (k *Keyring) ActiveVersion() int {
	return k.activeVersion
}

func (k *Keyring) Encrypt(plaintext, associatedData []byte) ([]byte, error) {
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		return nil, fmt.Errorf("generate data encryption key: %w", err)
	}
	masterAEAD, err := newGCM(k.keys[k.activeVersion])
	if err != nil {
		return nil, err
	}
	dataAEAD, err := newGCM(dataKey)
	if err != nil {
		return nil, err
	}
	wrappedNonce, wrappedKey, err := seal(masterAEAD, dataKey, append([]byte("sks-migration/dek/v1:"), associatedData...))
	if err != nil {
		return nil, err
	}
	payloadNonce, encryptedPayload, err := seal(dataAEAD, plaintext, associatedData)
	if err != nil {
		return nil, err
	}
	value := encryptedEnvelope{
		Version:          envelopeVersion,
		KeyVersion:       k.activeVersion,
		WrappedKeyNonce:  base64.RawStdEncoding.EncodeToString(wrappedNonce),
		WrappedKey:       base64.RawStdEncoding.EncodeToString(wrappedKey),
		PayloadNonce:     base64.RawStdEncoding.EncodeToString(payloadNonce),
		EncryptedPayload: base64.RawStdEncoding.EncodeToString(encryptedPayload),
	}
	return json.Marshal(value)
}

func (k *Keyring) Decrypt(encoded, associatedData []byte) ([]byte, error) {
	var value encryptedEnvelope
	if err := json.Unmarshal(encoded, &value); err != nil || value.Version != envelopeVersion {
		return nil, errors.New("invalid encrypted credential envelope")
	}
	masterKey, ok := k.keys[value.KeyVersion]
	if !ok {
		return nil, fmt.Errorf("credential master key version %d is unavailable", value.KeyVersion)
	}
	wrappedNonce, err := decodeEnvelopePart(value.WrappedKeyNonce)
	if err != nil {
		return nil, err
	}
	wrappedKey, err := decodeEnvelopePart(value.WrappedKey)
	if err != nil {
		return nil, err
	}
	payloadNonce, err := decodeEnvelopePart(value.PayloadNonce)
	if err != nil {
		return nil, err
	}
	encryptedPayload, err := decodeEnvelopePart(value.EncryptedPayload)
	if err != nil {
		return nil, err
	}
	masterAEAD, err := newGCM(masterKey)
	if err != nil {
		return nil, err
	}
	dataKey, err := masterAEAD.Open(nil, wrappedNonce, wrappedKey, append([]byte("sks-migration/dek/v1:"), associatedData...))
	if err != nil {
		return nil, errors.New("credential data key authentication failed")
	}
	dataAEAD, err := newGCM(dataKey)
	if err != nil {
		return nil, err
	}
	plaintext, err := dataAEAD.Open(nil, payloadNonce, encryptedPayload, associatedData)
	if err != nil {
		return nil, errors.New("credential payload authentication failed")
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

func seal(aead cipher.AEAD, plaintext, associatedData []byte) ([]byte, []byte, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("generate encryption nonce: %w", err)
	}
	return nonce, aead.Seal(nil, nonce, plaintext, associatedData), nil
}

func decodeEnvelopePart(value string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return nil, errors.New("invalid encrypted credential encoding")
	}
	return decoded, nil
}
