// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

// Package flarereport wires Pare's error reporting to the house Flare instance
// (Sentry-wire protocol). It mirrors hash/internal/flarereport: a no-op unless
// FLARE_DSN is set, so dev runs and self-hosts boot unchanged.
package flarereport

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	sentry "github.com/getsentry/sentry-go"
)

// scrubSensitive strips the request query string before an event leaves the
// process. Pare puts no secrets in the URL (session + CSRF ride in cookies,
// shield tokens never reach a URL), but the query string can carry a status
// flash (?msg=) and clearing it is cheap defense-in-depth. Applied to every
// event via BeforeSend so it covers panics and CaptureException alike.
func scrubSensitive(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event != nil && event.Request != nil {
		event.Request.QueryString = ""
		event.Request.Cookies = ""
	}
	return event
}

// InitFlare enables error reporting when FLARE_DSN is set (injected by the
// Hephaestus flare-provision deploy step). Without it this is a no-op.
func InitFlare(service, release string) bool {
	dsn := os.Getenv("FLARE_DSN")
	if dsn == "" {
		return false
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:        dsn,
		Release:    release,
		ServerName: service,
		BeforeSend: scrubSensitive,
	})
	if err != nil {
		slog.Warn("flare: error reporting disabled (sentry init failed)", "error", err)
		return false
	}
	slog.Info("flare: error reporting enabled", "service", service)
	return true
}

// FlareRecoverer captures panics to Flare and re-panics so chi's Recoverer still
// renders the 500. Mount it AFTER Recoverer so it sees the panic first. Safe to
// mount when InitFlare was a no-op.
func FlareRecoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				hub := sentry.CurrentHub().Clone()
				hub.Scope().SetRequest(r)
				hub.RecoverWithContext(r.Context(), rec)
				hub.Flush(2 * time.Second)
				panic(rec)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// CaptureErr reports a handled-but-should-page error to Flare. No-op when
// reporting is disabled.
func CaptureErr(err error) {
	if err == nil {
		return
	}
	sentry.CaptureException(err)
}

// Flush drains buffered events on shutdown.
func Flush() { sentry.Flush(2 * time.Second) }
