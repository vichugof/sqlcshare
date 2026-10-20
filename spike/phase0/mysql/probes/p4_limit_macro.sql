-- name: ListAuthorsLimitP4 :many
SELECT a.id, a.name, a.org_id, a.created_at
FROM authors a
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
ORDER BY a.created_at DESC
LIMIT sqlc.arg(page_size);
