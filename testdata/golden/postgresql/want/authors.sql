-- name: ListAuthors :many
-- sqlcshare: queries/authors.sql:1 (cte recent_orders, columns author_cols, where visible_author)
WITH /* sqlcshare */ recent_orders AS (SELECT o.* FROM orders o WHERE o.created_at > sqlc.narg(since)::timestamptz)
SELECT a.id, a.name, a.org_id, a.created_at, count(r.id) AS order_count
FROM authors a
LEFT JOIN recent_orders r ON r.author_id = a.id
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND a.name ILIKE sqlc.arg(pattern)
GROUP BY a.id;

-- name: CountListAuthors :one
-- sqlcshare: queries/authors.sql:2 (count of ListAuthors, cte recent_orders, columns author_cols, where visible_author)
WITH /* sqlcshare */ recent_orders AS (SELECT o.* FROM orders o WHERE o.created_at > sqlc.narg(since)::timestamptz)
SELECT count(*) FROM (SELECT a.id, a.name, a.org_id, a.created_at, count(r.id) AS order_count
FROM authors a
LEFT JOIN recent_orders r ON r.author_id = a.id
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND a.name ILIKE sqlc.arg(pattern)
GROUP BY a.id) AS sqlcshare_count;

-- name: CountAuthors :one
-- sqlcshare: queries/authors.sql:11 (where visible_author)
SELECT /* sqlcshare */ count(*) FROM authors a
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id));

-- name: GetAuthor :one
-- sqlcshare: queries/authors.sql:15 (columns author_cols, where visible_author)
SELECT /* sqlcshare */ a.id, a.name, a.org_id, a.created_at
FROM authors a
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND a.id = sqlc.arg(id);

-- name: ListAuthorsWithOrg :many
-- sqlcshare: queries/authors.sql:21 (columns author_cols, joins author_org, where visible_author)
SELECT /* sqlcshare */ a.id, a.name, a.org_id, a.created_at, o.name AS org_name
FROM authors a
JOIN orgs o ON o.id = a.org_id
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
ORDER BY a.created_at DESC
LIMIT sqlc.arg(page_size);

-- name: CountAuthorsWithOrg :one
-- sqlcshare: queries/authors.sql:22 (count of ListAuthorsWithOrg, joins author_org, where visible_author)
SELECT /* sqlcshare */ count(*) FROM authors a
JOIN orgs o ON o.id = a.org_id
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id));

-- name: ListAuthorsWithPeers :many
-- sqlcshare: queries/authors.sql:30 (columns author_cols, where visible_author, where visible_author)
SELECT /* sqlcshare */ a.id, a.name, a.org_id, a.created_at
FROM authors a
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND EXISTS (
    SELECT 1 FROM authors b
    WHERE (b.deleted_at IS NULL AND b.org_id = sqlc.arg(org_id))
      AND b.id <> a.id
  );

-- name: SearchAuthors :many
-- sqlcshare: queries/authors.sql:40 (columns author_cols, where visible_author, where name_filter)
SELECT /* sqlcshare */ a.id, a.name, a.org_id, a.created_at
FROM authors a
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND (a.name = sqlc.narg(name) OR sqlc.narg(name) IS NULL);

-- name: CountSearchAuthors :one
-- sqlcshare: queries/authors.sql:41 (count of SearchAuthors, where visible_author, where name_filter)
SELECT /* sqlcshare */ count(*) FROM authors a
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND (a.name = sqlc.narg(name) OR sqlc.narg(name) IS NULL);

-- name: ListAuthorsEmbed :many
-- sqlcshare: queries/authors.sql:47 (columns author_embed, joins author_org_left, where visible_author)
SELECT /* sqlcshare */ sqlc.embed(a), sqlc.embed(o)
FROM authors a
LEFT JOIN orgs o ON o.id = a.org_id
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id));
