// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

package shield

import (
	"context"
	"reflect"

	"github.com/brightinteraction/pare/internal/crypto"
)

// Store persists token ciphertext per session.
type Store interface {
	Put(ctx context.Context, sessionID, token, kind, ciphertext string) error
	Get(ctx context.Context, sessionID, token string) (string, error) // "" if absent
}

// Shield tokenizes and detokenizes with a 32-byte master key and a token store.
type Shield struct {
	dek    *crypto.DEK
	master []byte
	store  Store
}

// New builds a Shield. masterKey must be 32 bytes (PARE_SHIELD_KEY).
func New(masterKey []byte, store Store) (*Shield, error) {
	dek, err := crypto.NewDEKFrom(masterKey)
	if err != nil {
		return nil, err
	}
	m := make([]byte, len(masterKey))
	copy(m, masterKey)
	return &Shield{dek: dek, master: m, store: store}, nil
}

// Session binds a Shield to one MCP session id (so tokens are stable within a
// conversation but unlinkable across sessions).
type Session struct {
	sh  *Shield
	id  string
	key []byte
}

// Session returns a session-scoped tokenizer.
func (sh *Shield) Session(id string) *Session {
	return &Session{sh: sh, id: id, key: deriveSessionKey(sh.master, id)}
}

// Tokenize stores the value and returns its marker. Empty values pass through.
func (s *Session) Tokenize(ctx context.Context, kind Kind, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	tok := tokenID(s.key, kind, value)
	ct, err := s.sh.dek.EncryptField([]byte(value))
	if err != nil {
		return "", err
	}
	if err := s.sh.store.Put(ctx, s.id, tok, string(kind), ct); err != nil {
		return "", err
	}
	return marker(kind, tok), nil
}

// Resolve returns the plaintext for a bare token id, or "" if unknown.
func (s *Session) Resolve(ctx context.Context, token string) (string, error) {
	ct, err := s.sh.store.Get(ctx, s.id, token)
	if err != nil || ct == "" {
		return "", err
	}
	b, err := s.sh.dek.DecryptField(ct)
	return string(b), err
}

// ShieldStruct walks v (a pointer to a struct) and replaces every string field
// tagged shield:"tokenize,kind=X" with its marker, recursing into nested
// structs, slices and pointers.
func (s *Session) ShieldStruct(ctx context.Context, v any) error {
	return s.walk(ctx, reflect.ValueOf(v))
}

func (s *Session) walk(ctx context.Context, rv reflect.Value) error {
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return s.walk(ctx, rv.Elem())
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			if err := s.walk(ctx, rv.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < rv.NumField(); i++ {
			f := rv.Type().Field(i)
			fv := rv.Field(i)
			if kind, ok := parseTag(f.Tag.Get("shield")); ok {
				if fv.Kind() == reflect.String && fv.CanSet() {
					m, err := s.Tokenize(ctx, kind, fv.String())
					if err != nil {
						return err
					}
					if m != "" {
						fv.SetString(m)
					}
				}
				continue
			}
			switch fv.Kind() {
			case reflect.Struct, reflect.Slice, reflect.Array, reflect.Pointer, reflect.Interface:
				if err := s.walk(ctx, fv); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// UnshieldString resolves every [shield:...] marker in a string back to
// plaintext (used on the way in, if the LLM echoes a token).
func (s *Session) UnshieldString(ctx context.Context, in string) (string, error) {
	var firstErr error
	out := markerRe.ReplaceAllStringFunc(in, func(m string) string {
		sub := markerRe.FindStringSubmatch(m)
		val, err := s.Resolve(ctx, sub[2])
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return m
		}
		if val == "" {
			return m
		}
		return val
	})
	return out, firstErr
}

// MemStore is an in-memory Store for tests.
type MemStore struct{ m map[string]string }

// NewMemStore builds an empty in-memory store.
func NewMemStore() *MemStore { return &MemStore{m: map[string]string{}} }

func (s *MemStore) Put(_ context.Context, sessionID, token, _, ciphertext string) error {
	s.m[sessionID+"|"+token] = ciphertext
	return nil
}

func (s *MemStore) Get(_ context.Context, sessionID, token string) (string, error) {
	return s.m[sessionID+"|"+token], nil
}
