// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

package handler

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
)

const csrfCookieName = "pare_csrf"

// ctxCSRFKey holds the per-browser CSRF token for the current request.
type ctxCSRFKeyT int

const ctxCSRF ctxCSRFKeyT = 0

// csrfProtect implements synchronizer-token CSRF defence for the form UI. On
// every request it ensures a random token cookie exists and exposes it in the
// context (so GET handlers can render it into a hidden field). On unsafe methods
// it requires the submitted "csrf" form field to match that cookie, in constant
// time. Combined with the SameSite=Strict session + token cookies this is
// defence in depth: a cross-site POST carries neither cookie nor a valid token.
//
// It deliberately guards login and setup too (login CSRF is real), which is why
// the token lives in its own cookie rather than in the authenticated session.
func (s *Server) csrfProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if c, err := r.Cookie(csrfCookieName); err == nil && c.Value != "" {
			token = c.Value
		}
		if token == "" {
			token = randToken()
			http.SetCookie(w, &http.Cookie{
				Name:     csrfCookieName,
				Value:    token,
				Path:     "/",
				HttpOnly: true,
				Secure:   s.SecureCookies,
				SameSite: http.SameSiteStrictMode,
				MaxAge:   12 * 60 * 60,
			})
		}

		if !safeMethod(r.Method) {
			// ParseForm caches into r.PostForm; handlers re-reading via
			// PostFormValue get the cached parse, so this is not double work.
			_ = r.ParseForm()
			got := r.PostFormValue("csrf")
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				http.Error(w, "invalid CSRF token", http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxCSRF, token)))
	})
}

func safeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func csrfToken(r *http.Request) string {
	t, _ := r.Context().Value(ctxCSRF).(string)
	return t
}

func randToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
