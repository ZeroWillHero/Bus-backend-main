-- name: CreateOperator :one
-- id is generated application-side (not DEFAULT gen_random_uuid()) so the
-- caller can SET LOCAL app.current_operator_id to it *before* running this
-- insert — required for the RLS SELECT policy backing this query's
-- RETURNING clause to see the row it just created (DATABASE_DESIGN.md §5:
-- a brand-new row's id can never match a session's pre-existing tenant
-- context, so self-registration must establish that context itself).
INSERT INTO ops.operators (
    id, name, business_reg_no, contact_email, contact_phone, registration_doc_url
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: GetOperatorByID :one
SELECT * FROM ops.operators WHERE id = $1;

-- name: GetOperatorByBusinessRegNo :one
SELECT * FROM ops.operators WHERE business_reg_no = $1;

-- name: ListOperatorsByStatus :many
SELECT * FROM ops.operators
WHERE status = $1
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR created_at < sqlc.narg('cursor_created_at')::timestamptz
    OR (created_at = sqlc.narg('cursor_created_at')::timestamptz AND id < sqlc.narg('cursor_id')::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: UpdateOperatorStatus :one
UPDATE ops.operators
SET status = $2,
    approved_at = CASE WHEN $2 = 'approved' THEN now() ELSE approved_at END
WHERE id = $1
RETURNING *;
