-- name: ListAuthorsByIDsP1b :many
SELECT a.id, a.name, a.org_id, a.created_at
FROM authors a
WHERE (a.id IN (sqlc.slice(ids)))
  AND (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id));
