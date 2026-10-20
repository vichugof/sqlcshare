-- name: CountAuthorsD5 :one
SELECT count(*) FROM authors a
WHERE sqlc.fragment(visible_author);
