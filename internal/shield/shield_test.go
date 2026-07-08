// SPDX-License-Identifier: AGPL-3.0-or-later
package shield

import (
	"context"
	"strings"
	"testing"

	"github.com/brightinteraction/pare/internal/crypto"
)

func newShield(t *testing.T) (*Shield, *MemStore) {
	t.Helper()
	key, _ := crypto.NewDEK()
	store := NewMemStore()
	sh, err := New(key, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return sh, store
}

func TestTokenizeRoundTrip(t *testing.T) {
	sh, store := newShield(t)
	ctx := context.Background()
	s := sh.Session("sess-1")

	m, err := s.Tokenize(ctx, KindOrgNr, "556677-8899")
	if err != nil {
		t.Fatalf("tokenize: %v", err)
	}
	if !markerRe.MatchString(m) {
		t.Fatalf("not a marker: %q", m)
	}
	if strings.Contains(m, "556677") {
		t.Fatal("marker leaks the value")
	}
	// vault holds ciphertext, never plaintext
	for _, ct := range store.m {
		if strings.Contains(ct, "556677") {
			t.Fatal("vault stored plaintext")
		}
	}
	sub := markerRe.FindStringSubmatch(m)
	got, err := s.Resolve(ctx, sub[2])
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "556677-8899" {
		t.Fatalf("resolve mismatch: %q", got)
	}
}

func TestDeterministicAndSessionScoped(t *testing.T) {
	sh, _ := newShield(t)
	ctx := context.Background()
	a := sh.Session("A")
	b := sh.Session("B")

	m1, _ := a.Tokenize(ctx, KindName, "Advokatbyrån Nord AB")
	m2, _ := a.Tokenize(ctx, KindName, "Advokatbyrån Nord AB")
	if m1 != m2 {
		t.Fatal("same value in same session produced different tokens")
	}
	m3, _ := b.Tokenize(ctx, KindName, "Advokatbyrån Nord AB")
	if m3 == m1 {
		t.Fatal("token id is linkable across sessions")
	}
}

type cpView struct {
	Name     string `shield:"tokenize,kind=name"`
	OrgNr    string `shield:"tokenize,kind=orgnr"`
	AmountKr string // NOT tagged; must stay visible to the AI
	Lines    []struct {
		Note string `shield:"tokenize,kind=name"`
	}
}

func TestShieldStruct(t *testing.T) {
	sh, _ := newShield(t)
	ctx := context.Background()
	s := sh.Session("s")

	v := &cpView{Name: "Kund AB", OrgNr: "556100-2222", AmountKr: "12500,00"}
	v.Lines = append(v.Lines, struct {
		Note string `shield:"tokenize,kind=name"`
	}{Note: "Hemligt"})

	if err := s.ShieldStruct(ctx, v); err != nil {
		t.Fatalf("shield struct: %v", err)
	}
	if !markerRe.MatchString(v.Name) || !markerRe.MatchString(v.OrgNr) {
		t.Fatalf("identity fields not tokenized: %+v", v)
	}
	if v.AmountKr != "12500,00" {
		t.Fatalf("amount was tokenized (should stay visible): %q", v.AmountKr)
	}
	if !markerRe.MatchString(v.Lines[0].Note) {
		t.Fatalf("nested slice field not tokenized: %q", v.Lines[0].Note)
	}

	// round trip a marker back
	back, err := s.UnshieldString(ctx, "Kund: "+v.Name)
	if err != nil {
		t.Fatalf("unshield: %v", err)
	}
	if back != "Kund: Kund AB" {
		t.Fatalf("unshield mismatch: %q", back)
	}
}
