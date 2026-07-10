// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package auth handles operator login for the web UI: bcrypt passwords and
// server-side sessions with an HttpOnly, Secure, SameSite=Strict cookie.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	gen "github.com/bright-interaction/pare/internal/db/generated"
)

// CookieName is the session cookie name.
const CookieName = "pare_session"

const sessionTTL = 12 * time.Hour

// ErrInvalidCredentials is returned for a bad email/password.
var ErrInvalidCredentials = errors.New("invalid credentials")

// Auth is the authentication service.
type Auth struct {
	q             *gen.Queries
	secureCookies bool
}

// New builds an Auth. secureCookies should be true in production (HTTPS).
func New(q *gen.Queries, secureCookies bool) *Auth {
	return &Auth{q: q, secureCookies: secureCookies}
}

// HasUsers reports whether any operator account exists yet.
func (a *Auth) HasUsers(ctx context.Context) (bool, error) {
	n, err := a.q.CountUsers(ctx)
	return n > 0, err
}

// CreateUser registers an operator with a bcrypt-hashed password and a role
// ("owner" or "viewer"; anything else becomes "owner").
func (a *Auth) CreateUser(ctx context.Context, email, password, role string) error {
	if role != "viewer" {
		role = "owner"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = a.q.InsertUser(ctx, gen.InsertUserParams{Email: email, PasswordHash: string(hash), Role: role})
	return err
}

// Login verifies credentials and creates a session, returning its token.
func (a *Auth) Login(ctx context.Context, email, password string) (string, error) {
	u, err := a.q.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrInvalidCredentials
		}
		return "", err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return "", ErrInvalidCredentials
	}
	token := randToken()
	err = a.q.InsertSession(ctx, gen.InsertSessionParams{
		Token:     token,
		UserID:    u.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(sessionTTL), Valid: true},
	})
	return token, err
}

// Logout deletes a session.
func (a *Auth) Logout(ctx context.Context, token string) error {
	return a.q.DeleteSession(ctx, token)
}

// SessionInfo is the authenticated user.
type SessionInfo struct {
	UserID uuid.UUID
	Email  string
	Role   string
}

// IsOwner reports whether the session may perform state-changing actions.
func (s SessionInfo) IsOwner() bool { return s.Role != "viewer" }

// Validate returns the session for a token, if valid and unexpired.
func (a *Auth) Validate(ctx context.Context, token string) (SessionInfo, bool) {
	if token == "" {
		return SessionInfo{}, false
	}
	row, err := a.q.GetSession(ctx, token)
	if err != nil {
		return SessionInfo{}, false
	}
	if !row.ExpiresAt.Valid || row.ExpiresAt.Time.Before(time.Now()) {
		return SessionInfo{}, false
	}
	return SessionInfo{UserID: row.UserID, Email: row.Email, Role: row.Role}, true
}

// SetCookie writes the session cookie.
func (a *Auth) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secureCookies,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(sessionTTL),
	})
}

// ClearCookie removes the session cookie.
func (a *Auth) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})
}

func randToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
