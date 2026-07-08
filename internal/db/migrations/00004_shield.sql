-- +goose Up
-- +goose StatementBegin
-- Shield token vault. When the MCP returns a counterparty identity to the LLM
-- it is replaced by a stable per-session token; the plaintext is stored here as
-- AES-256-GCM ciphertext (PARE_SHIELD_KEY) and only ever resolved back inside
-- the app, never sent to Anthropic. Token ids are deterministic per session so
-- the same identity reads as the same token across a conversation.
CREATE TABLE shield_tokens (
    session_id TEXT NOT NULL,
    token      TEXT NOT NULL,
    kind       TEXT NOT NULL,
    ciphertext TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, token)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS shield_tokens;
-- +goose StatementEnd
