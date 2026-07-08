// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package crypto provides envelope encryption for Pare's at-rest identity and
// PII columns. A single key-encryption key (KEK, from PARE_MASTER_KEY) wraps a
// per-company data-encryption key (DEK); the DEK encrypts individual field
// values. Amounts, account codes and dates are never encrypted so the ledger
// stays queryable; only counterparty identities and PII are protected.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// KeySize is the AES-256 key length in bytes.
const KeySize = 32

// ErrKeySize is returned when a key is not exactly KeySize bytes.
var ErrKeySize = fmt.Errorf("crypto: key must be %d bytes", KeySize)

// ErrCiphertext is returned when stored ciphertext is malformed.
var ErrCiphertext = errors.New("crypto: ciphertext too short")

// KEK is the key-encryption key. It only wraps DEKs; it never encrypts domain
// data directly.
type KEK struct{ key []byte }

// NewKEK builds a KEK from a 32-byte master key.
func NewKEK(master []byte) (*KEK, error) {
	if len(master) != KeySize {
		return nil, ErrKeySize
	}
	return &KEK{key: clone(master)}, nil
}

// NewDEK returns a fresh random 32-byte data-encryption key.
func NewDEK() ([]byte, error) {
	dek := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, err
	}
	return dek, nil
}

// WrapDEK encrypts a DEK under the KEK for storage alongside the company row.
func (k *KEK) WrapDEK(dek []byte) (string, error) {
	if len(dek) != KeySize {
		return "", ErrKeySize
	}
	return seal(k.key, dek)
}

// UnwrapDEK recovers a plaintext DEK from its wrapped form.
func (k *KEK) UnwrapDEK(wrapped string) ([]byte, error) {
	return open(k.key, wrapped)
}

// DEK encrypts and decrypts individual field values for one company.
type DEK struct{ key []byte }

// NewDEKFrom wraps raw key bytes (already unwrapped) as a DEK.
func NewDEKFrom(raw []byte) (*DEK, error) {
	if len(raw) != KeySize {
		return nil, ErrKeySize
	}
	return &DEK{key: clone(raw)}, nil
}

// EncryptField returns base64(nonce||ciphertext) for a plaintext field value.
func (d *DEK) EncryptField(plaintext []byte) (string, error) {
	return seal(d.key, plaintext)
}

// DecryptField reverses EncryptField.
func (d *DEK) DecryptField(ciphertext string) ([]byte, error) {
	return open(d.key, ciphertext)
}

func seal(key, plaintext []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func open(key []byte, encoded string) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return nil, ErrCiphertext
	}
	return gcm.Open(nil, raw[:ns], raw[ns:], nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func clone(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
