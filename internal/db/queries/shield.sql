-- name: UpsertShieldToken :exec
INSERT INTO shield_tokens (session_id, token, kind, ciphertext)
VALUES ($1, $2, $3, $4)
ON CONFLICT (session_id, token) DO NOTHING;

-- name: GetShieldToken :one
SELECT ciphertext FROM shield_tokens WHERE session_id = $1 AND token = $2;
