-- name: FineD5b :one
SELECT count(*) FROM authors a
WHERE a.id = sqlc.arg(id);

-- name: CountAuthorsD5b :one
SELECT count(*) FROM authors a
WHERE sqlc.fragment(visible_author);
