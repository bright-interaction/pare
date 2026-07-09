// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package email sends transactional mail over SMTP (invoice delivery, payment
// reminders). Dependency-free (stdlib net/smtp + crypto/tls) so Pare stays a
// single static binary and works against any SMTP server (Resend/SES in prod,
// mailhog in dev). Mirrors hash/flare's mailer, plus PDF attachment support.
package email

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Mailer holds resolved SMTP settings. The zero value (host/from empty) is
// disabled: Send returns ErrDisabled so callers no-op cleanly when SMTP is not
// configured (e.g. before Tom seeds PARE_SMTP_*).
type Mailer struct {
	host, user, pass, from, fromName, tlsMode string
	port                                      int
}

// ErrDisabled is returned when no SMTP host/from is configured.
var ErrDisabled = fmt.Errorf("email: SMTP not configured")

// New builds a Mailer from resolved config.
func New(host string, port int, user, pass, from, fromName, tlsMode string) *Mailer {
	if tlsMode == "" {
		tlsMode = "starttls"
	}
	if fromName == "" {
		fromName = "Pare"
	}
	return &Mailer{host: host, port: port, user: user, pass: pass, from: from, fromName: fromName, tlsMode: tlsMode}
}

// Enabled reports whether the mailer can send.
func (m *Mailer) Enabled() bool { return m != nil && m.host != "" && m.from != "" }

// Attachment is a file to attach (e.g. an invoice PDF).
type Attachment struct {
	Name    string
	Mime    string
	Content []byte
}

// Send delivers a text+HTML message, with optional attachments, to one recipient.
func (m *Mailer) Send(to, subject, htmlBody, textBody string, attachments ...Attachment) error {
	if !m.Enabled() {
		return ErrDisabled
	}
	if to == "" {
		return fmt.Errorf("email: no recipient")
	}
	msg := m.build(to, subject, textBody, htmlBody, attachments)
	addr := net.JoinHostPort(m.host, fmt.Sprintf("%d", m.port))
	var auth smtp.Auth
	if m.user != "" {
		auth = smtp.PlainAuth("", m.user, m.pass, m.host)
	}
	if m.tlsMode == "tls" {
		return m.sendImplicitTLS(addr, auth, to, msg)
	}
	return smtp.SendMail(addr, auth, m.from, []string{to}, msg)
}

func (m *Mailer) sendImplicitTLS(addr string, auth smtp.Auth, to string, msg []byte) error {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, &tls.Config{ServerName: m.host})
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return err
	}
	defer c.Close()
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(m.from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(msg); err != nil {
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func (m *Mailer) build(to, subject, text, html string, atts []Attachment) []byte {
	mixed := "pare-mixed-7f3a"
	alt := "pare-alt-9d7c"
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s <%s>\r\n", m.fromName, m.from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", mixed)

	// Body part (multipart/alternative: text + HTML).
	fmt.Fprintf(&b, "--%s\r\n", mixed)
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", alt)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n\r\n", alt, text)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n\r\n", alt, html)
	fmt.Fprintf(&b, "--%s--\r\n\r\n", alt)

	// Attachments (base64).
	for _, a := range atts {
		mime := a.Mime
		if mime == "" {
			mime = "application/octet-stream"
		}
		fmt.Fprintf(&b, "--%s\r\n", mixed)
		fmt.Fprintf(&b, "Content-Type: %s\r\n", mime)
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		fmt.Fprintf(&b, "Content-Disposition: attachment; filename=%q\r\n\r\n", a.Name)
		enc := base64.StdEncoding.EncodeToString(a.Content)
		for i := 0; i < len(enc); i += 76 {
			end := i + 76
			if end > len(enc) {
				end = len(enc)
			}
			b.WriteString(enc[i:end])
			b.WriteString("\r\n")
		}
		b.WriteString("\r\n")
	}
	fmt.Fprintf(&b, "--%s--\r\n", mixed)
	return []byte(b.String())
}
