// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package render turns invoices into PDF via Gotenberg's Chromium converter.
// The HTTP client is a trimmed port of hash/internal/render.
package render

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

// Gotenberg wraps the HTTP client used to talk to a Gotenberg instance.
type Gotenberg struct {
	BaseURL string
	HTTP    *http.Client
}

// NewGotenberg builds a client with a 60-second timeout.
func NewGotenberg(baseURL string) *Gotenberg {
	return &Gotenberg{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Ping confirms Gotenberg is reachable via its /health endpoint.
func (g *Gotenberg) Ping(ctx context.Context) error {
	if g.BaseURL == "" {
		return fmt.Errorf("gotenberg base url not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.BaseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("gotenberg /health returned %d", resp.StatusCode)
	}
	return nil
}

// HTMLToPDF posts an index.html to Gotenberg and returns the rendered PDF.
// A4 with 15mm margins, retrying 5xx/408/429 up to three times.
func (g *Gotenberg) HTMLToPDF(ctx context.Context, html string) ([]byte, error) {
	if g.BaseURL == "" {
		return nil, fmt.Errorf("gotenberg base url not configured")
	}
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	if err := writeFormFile(mw, "files", "index.html", "text/html", []byte(html)); err != nil {
		return nil, err
	}
	_ = mw.WriteField("paperWidth", "8.27")   // A4 inches
	_ = mw.WriteField("paperHeight", "11.69")  // A4 inches
	_ = mw.WriteField("marginTop", "0.59")     // ~15mm
	_ = mw.WriteField("marginBottom", "0.59")
	_ = mw.WriteField("marginLeft", "0.59")
	_ = mw.WriteField("marginRight", "0.59")
	_ = mw.WriteField("printBackground", "true")
	if err := mw.Close(); err != nil {
		return nil, err
	}

	url := g.BaseURL + "/forms/chromium/convert/html"
	frozen := body.Bytes()
	contentType := mw.FormDataContentType()

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 200 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(frozen))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", contentType)
		resp, err := g.HTTP.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("gotenberg request: %w", err)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			return io.ReadAll(resp.Body)
		}
		out, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode < 500 && resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusTooManyRequests {
			return nil, fmt.Errorf("gotenberg returned %d: %s", resp.StatusCode, string(out))
		}
		lastErr = fmt.Errorf("gotenberg returned %d: %s", resp.StatusCode, string(out))
	}
	return nil, lastErr
}

func writeFormFile(mw *multipart.Writer, field, filename, mime string, contents []byte) error {
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, filename))
	h.Set("Content-Type", mime)
	w, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	_, err = w.Write(contents)
	return err
}
