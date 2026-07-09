-- name: UpsertShieldToken :exec
INSERT INTO shield_tokens (session_id, token, kind, ciphertext)
VALUES ($1, $2, $3, $4)
ON CONFLICT (session_id, token) DO NOTHING;

-- name: GetShieldToken :one
SELECT ciphertext FROM shield_tokens WHERE session_id = $1 AND token = $2;

-- name: DeleteOldShieldTokens :execrows
-- TTL sweep: bounds how long any tokenized value (including a GDPR-erased
-- counterparty's identity captured in a prior MCP session) remains resolvable.
-- Targeted per-identity purge is infeasible: tokens are per-session HMAC ids and
-- the ciphertext uses a random nonce, so neither can be matched to an identity.
DELETE FROM shield_tokens WHERE created_at < $1;
