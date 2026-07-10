// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
package crypto

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	k, err := NewDEK()
	if err != nil {
		t.Fatalf("NewDEK: %v", err)
	}
	return k
}

func TestKEKWrapRoundTrip(t *testing.T) {
	master := testKey(t)
	kek, err := NewKEK(master)
	if err != nil {
		t.Fatalf("NewKEK: %v", err)
	}
	dek, _ := NewDEK()
	wrapped, err := kek.WrapDEK(dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	got, err := kek.UnwrapDEK(wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Fatal("unwrapped DEK does not match original")
	}
}

func TestFieldRoundTrip(t *testing.T) {
	dek, err := NewDEKFrom(testKey(t))
	if err != nil {
		t.Fatalf("NewDEKFrom: %v", err)
	}
	plain := []byte("Advokatbyrån Nord AB, orgnr 556677-8899")
	ct, err := dek.EncryptField(plain)
	if err != nil {
		t.Fatalf("EncryptField: %v", err)
	}
	if bytes.Contains([]byte(ct), []byte("556677")) {
		t.Fatal("ciphertext leaks plaintext")
	}
	got, err := dek.DecryptField(ct)
	if err != nil {
		t.Fatalf("DecryptField: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestNonceIsRandom(t *testing.T) {
	dek, _ := NewDEKFrom(testKey(t))
	a, _ := dek.EncryptField([]byte("same"))
	b, _ := dek.EncryptField([]byte("same"))
	if a == b {
		t.Fatal("identical ciphertext for repeated plaintext (nonce reuse)")
	}
}

func TestTamperDetected(t *testing.T) {
	dek, _ := NewDEKFrom(testKey(t))
	ct, _ := dek.EncryptField([]byte("integrity"))
	raw, _ := base64.StdEncoding.DecodeString(ct)
	raw[len(raw)-1] ^= 0xff // flip a byte in the GCM tag
	if _, err := dek.DecryptField(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("tampered ciphertext decrypted without error")
	}
}

func TestKeySizeValidated(t *testing.T) {
	if _, err := NewKEK([]byte("short")); err != ErrKeySize {
		t.Fatalf("want ErrKeySize, got %v", err)
	}
	if _, err := NewDEKFrom(make([]byte, 16)); err != ErrKeySize {
		t.Fatalf("want ErrKeySize, got %v", err)
	}
}
