-- name: SearchAuthorsD4 :many
SELECT a.id, a.name, a.org_id, a.created_at FROM authors a WHERE (a.deleted_at IS NULL AND a.org_idd = sqlc.arg(org_id)) AND (a.name = sqlc.narg(name) OR sqlc.narg(name) IS NULL);
