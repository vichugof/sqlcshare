-- name: FirstD7 :one
SELECT a.id, a.nam FROM authors a WHERE a.id = sqlc.arg(id);

-- name: SecondD7 :one
SELECT a.id, a.name FROM authors a WHERE a.idd = sqlc.arg(id);
