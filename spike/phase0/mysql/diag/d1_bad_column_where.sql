-- name: GetAuthorD1 :one
-- sqlcshare: queries/authors.sql:16 (columns author_cols, where visible_author)
SELECT a.id, a.name, a.org_id, a.created_at
FROM authors a
WHERE (a.delted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND a.id = sqlc.arg(id);
