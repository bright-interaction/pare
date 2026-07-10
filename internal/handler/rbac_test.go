// SPDX-License-Identifier: AGPL-3.0-or-later
package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bright-interaction/pare/internal/auth"
)

func TestBlockViewerWrites(t *testing.T) {
	s := &Server{}
	served := false
	h := s.blockViewerWrites(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
	}))
	with := func(method, role string) *http.Request {
		req := httptest.NewRequest(method, "/invoices", nil)
		return req.WithContext(context.WithValue(req.Context(), ctxSession, auth.SessionInfo{Role: role}))
	}
	cases := []struct {
		method, role string
		want         int
	}{
		{http.MethodGet, "viewer", 200},  // viewer can read
		{http.MethodPost, "owner", 200},  // owner can write
		{http.MethodPost, "viewer", 403}, // viewer cannot write
		{http.MethodDelete, "viewer", 403},
	}
	for _, c := range cases {
		served = false
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, with(c.method, c.role))
		if rec.Code != c.want {
			t.Errorf("%s role=%s: code %d, want %d", c.method, c.role, rec.Code, c.want)
		}
		if c.want == 403 && served {
			t.Errorf("%s role=%s reached handler despite 403", c.method, c.role)
		}
	}
}
