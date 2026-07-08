// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package shield tokenizes counterparty identities and PII at the MCP boundary
// so the AI only ever sees stable opaque tokens, never a real name, org number,
// personnummer or IBAN. Plaintext is kept as AES-256-GCM ciphertext in a token
// vault (PARE_SHIELD_KEY) and resolved back only inside the app. This is Pare's
// implementation of the LLM-boundary-tokenization pattern (see Shield in the
// dockyard/brightcrm estate). Amounts and account codes are deliberately NOT
// tokenized so the assistant can still reconcile and prepare moms.
package shield

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Kind labels the type of a tokenized value.
type Kind string

const (
	KindName         Kind = "name"
	KindOrgNr        Kind = "orgnr"
	KindPersonnummer Kind = "personnummer"
	KindIBAN         Kind = "iban"
	KindAddress      Kind = "address"
	KindEmail        Kind = "email"
	KindPhone        Kind = "phone"
)

// PIIFields are field names (lowercased) that MUST be shield-tagged on any MCP
// response struct. The completeness guard fails the build if one is untagged.
var PIIFields = map[string]bool{
	"name": true, "orgnr": true, "personnummer": true,
	"iban": true, "address": true, "email": true, "phone": true,
}

var markerRe = regexp.MustCompile(`\[shield:([a-z]+):(tok_[0-9a-f]+)\]`)

func marker(kind Kind, token string) string {
	return "[shield:" + string(kind) + ":" + token + "]"
}

// tokenID derives a stable per-session token id from the value.
func tokenID(sessionKey []byte, kind Kind, value string) string {
	mac := hmac.New(sha256.New, sessionKey)
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return "tok_" + hex.EncodeToString(mac.Sum(nil))[:12]
}

func deriveSessionKey(master []byte, sessionID string) []byte {
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte("pare/shield/session/" + sessionID))
	return mac.Sum(nil)
}

// parseTag reads a `shield:"tokenize,kind=orgnr"` struct tag.
func parseTag(tag string) (Kind, bool) {
	if tag == "" || tag == "-" {
		return "", false
	}
	parts := strings.Split(tag, ",")
	if parts[0] != "tokenize" {
		return "", false
	}
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, "kind=") {
			return Kind(strings.TrimPrefix(p, "kind=")), true
		}
	}
	return "", false
}
