// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) Bright Interaction

// Package mcp exposes Pare over the Model Context Protocol (JSON-RPC 2.0) so an
// AI assistant can run the books. Every tool response passes through Shield at
// this boundary: counterparty identities become opaque tokens, amounts and
// account codes stay visible. The MCP requires PARE_SHIELD_KEY; without it the
// server is not mounted (see cmd/server).
package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	gen "github.com/brightinteraction/pare/internal/db/generated"
	"github.com/brightinteraction/pare/internal/shield"
	"github.com/brightinteraction/pare/internal/store"
)

// Server is the Pare MCP endpoint.
type Server struct {
	store  *store.Store
	shield *shield.Shield
	apiKey string
	tools  map[string]tool
	order  []string
}

// New builds the MCP server. shieldKey must be a 32-byte key; apiKey gates
// access. Returns an error if the shield key is invalid.
func New(st *store.Store, pool *pgxpool.Pool, shieldKey []byte, apiKey string) (*Server, error) {
	sh, err := shield.New(shieldKey, shield.NewPgStore(gen.New(pool)))
	if err != nil {
		return nil, err
	}
	s := &Server{store: st, shield: sh, apiKey: apiKey, tools: map[string]tool{}}
	s.register()
	return s, nil
}

// toolCtx is passed to each tool handler.
type toolCtx struct {
	store   *store.Store
	sess    *shield.Session
	company uuid.UUID
}

// tool is one MCP tool.
type tool struct {
	name  string
	desc  string
	write bool
	// schema is the JSON input schema advertised in tools/list.
	schema map[string]any
	// proto is a pointer to a zero result value, used by the completeness guard.
	proto any
	run   func(ctx context.Context, tc toolCtx, args json.RawMessage) (any, error)
}

func (s *Server) add(t tool) {
	s.tools[t.name] = t
	s.order = append(s.order, t.name)
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Handler returns the HTTP handler for the /mcp endpoint.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req rpcReq
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeJSON(w, rpcResp{JSONRPC: "2.0", Error: &rpcErr{Code: -32700, Message: "parse error"}})
			return
		}
		resp := s.dispatch(r.Context(), r, req)
		if resp == nil { // notification, no reply
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, *resp)
	})
}

func (s *Server) authed(r *http.Request) bool {
	if s.apiKey == "" {
		return false
	}
	got := r.Header.Get("X-Api-Key")
	if got == "" {
		const p = "Bearer "
		if h := r.Header.Get("Authorization"); len(h) > len(p) && h[:len(p)] == p {
			got = h[len(p):]
		}
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.apiKey)) == 1
}

func (s *Server) dispatch(ctx context.Context, r *http.Request, req rpcReq) *rpcResp {
	ok := func(result any) *rpcResp { return &rpcResp{JSONRPC: "2.0", ID: req.ID, Result: result} }
	fail := func(code int, msg string) *rpcResp {
		return &rpcResp{JSONRPC: "2.0", ID: req.ID, Error: &rpcErr{Code: code, Message: msg}}
	}

	switch req.Method {
	case "initialize":
		return ok(map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "pare", "version": "0.1"},
		})
	case "notifications/initialized", "notifications/cancelled":
		return nil
	case "tools/list":
		return ok(map[string]any{"tools": s.toolList()})
	case "tools/call":
		return s.callTool(ctx, r, req, ok, fail)
	default:
		return fail(-32601, "method not found: "+req.Method)
	}
}

func (s *Server) toolList() []map[string]any {
	out := make([]map[string]any, 0, len(s.order))
	for _, name := range s.order {
		t := s.tools[name]
		out = append(out, map[string]any{
			"name":        t.name,
			"description": t.desc,
			"inputSchema": t.schema,
			"annotations": map[string]any{"readOnlyHint": !t.write},
		})
	}
	return out
}

func (s *Server) callTool(ctx context.Context, r *http.Request, req rpcReq, ok func(any) *rpcResp, fail func(int, string) *rpcResp) *rpcResp {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return fail(-32602, "invalid params")
	}
	t, found := s.tools[p.Name]
	if !found {
		return fail(-32602, "unknown tool: "+p.Name)
	}
	company, err := s.store.DefaultCompany(ctx)
	if err != nil {
		return toolError(ok, "no company configured yet")
	}
	sess := s.shield.Session(sessionID(r))
	res, err := t.run(ctx, toolCtx{store: s.store, sess: sess, company: company}, p.Arguments)
	if err != nil {
		return toolError(ok, err.Error())
	}
	// Tokenize identities before the result crosses to the LLM.
	if err := sess.ShieldStruct(ctx, res); err != nil {
		return toolError(ok, "shield error")
	}
	body, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return toolError(ok, "encode error")
	}
	return ok(map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(body)}},
	})
}

// toolError returns an MCP tools/call result flagged isError (not a JSON-RPC
// protocol error), which is how the spec surfaces tool failures to the model.
func toolError(ok func(any) *rpcResp, msg string) *rpcResp {
	return ok(map[string]any{
		"isError": true,
		"content": []map[string]any{{"type": "text", "text": msg}},
	})
}

func sessionID(r *http.Request) string {
	if id := r.Header.Get("Mcp-Session-Id"); id != "" {
		return id
	}
	return "default"
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
