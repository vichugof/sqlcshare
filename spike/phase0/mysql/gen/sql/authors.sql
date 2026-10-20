-- name: ListAuthors :many
-- sqlcshare: queries/authors.sql:3 (cte recent_orders, columns author_cols, where visible_author)
WITH recent_orders AS (SELECT o.* FROM orders o WHERE o.created_at > CAST(sqlc.narg(since) AS DATETIME))
SELECT /* sqlcshare */ a.id, a.name, a.org_id, a.created_at, count(r.id) AS order_count
FROM authors a
LEFT JOIN recent_orders r ON r.author_id = a.id
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND a.name LIKE sqlc.arg(pattern)
GROUP BY a.id;

-- name: CountAuthors :one
-- sqlcshare: queries/authors.sql:12 (where visible_author)
SELECT count(*) FROM authors a
/* sqlcshare:marker-at-column-zero */
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id));

-- name: GetAuthor :one
-- sqlcshare: queries/authors.sql:16 (columns author_cols, where visible_author)
SELECT a.id, a.name, a.org_id, a.created_at
FROM authors a
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND a.id = sqlc.arg(id);

-- name: ListAuthorsWithOrg :many
-- sqlcshare: queries/authors.sql:22 (columns author_cols, joins author_org, where visible_author)
SELECT a.id, a.name, a.org_id, a.created_at, o.name AS org_name
FROM authors a
JOIN orgs o ON o.id = a.org_id
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
ORDER BY a.created_at DESC
LIMIT ?;

-- name: ListAuthorsWithPeers :many
-- sqlcshare: queries/authors.sql:30 (columns author_cols, where visible_author, where visible_author)
SELECT a.id, a.name, a.org_id, a.created_at
FROM authors a
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND EXISTS (
    SELECT 1 FROM authors b
    WHERE (b.deleted_at IS NULL AND b.org_id = sqlc.arg(org_id))
      AND b.id <> a.id
  );

-- name: SearchAuthors :many
-- sqlcshare: queries/authors.sql:40 (columns author_cols, where visible_author, where name_filter)
SELECT a.id, a.name, a.org_id, a.created_at
FROM authors a
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND (a.name = sqlc.narg(name) OR sqlc.narg(name) IS NULL);

-- name: ListAuthorsEmbed :many
-- sqlcshare: queries/authors.sql:46 (columns author_embed, joins author_org_left, where visible_author)
SELECT sqlc.embed(a), sqlc.embed(o)
FROM authors a
LEFT JOIN orgs o ON o.id = a.org_id
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id));
