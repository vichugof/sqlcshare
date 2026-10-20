-- Source form (design draft). NOT listed in sqlc.yaml; the expander writes gen/sql/authors.sql.

-- name: ListAuthors :many
WITH /*%cte recent_orders*/ recent_orders AS (SELECT o.* FROM orders o WHERE FALSE) /*%end*/
SELECT /*%columns author_cols(a)*/ a.* /*%end*/, count(r.id) AS order_count
FROM authors a
LEFT JOIN recent_orders r ON r.author_id = a.id
WHERE /*%where visible_author(a)*/ TRUE /*%end*/
  AND a.name LIKE sqlc.arg(pattern)
GROUP BY a.id;

-- name: CountAuthors :one
SELECT count(*) FROM authors a
WHERE /*%where visible_author(a)*/ TRUE /*%end*/;

-- name: GetAuthor :one
SELECT /*%columns author_cols(a)*/ a.* /*%end*/
FROM authors a
WHERE /*%where visible_author(a)*/ TRUE /*%end*/
  AND a.id = sqlc.arg(id);

-- name: ListAuthorsWithOrg :many
SELECT /*%columns author_cols(a)*/ a.* /*%end*/, o.name AS org_name
FROM authors a
/*%joins author_org(a, o)*/ JOIN orgs o ON o.id = a.org_id /*%end*/
WHERE /*%where visible_author(a)*/ TRUE /*%end*/
ORDER BY a.created_at DESC
LIMIT sqlc.arg(page_size);

-- name: ListAuthorsWithPeers :many
SELECT /*%columns author_cols(a)*/ a.* /*%end*/
FROM authors a
WHERE /*%where visible_author(a)*/ TRUE /*%end*/
  AND EXISTS (
    SELECT 1 FROM authors b
    WHERE /*%where visible_author(b)*/ TRUE /*%end*/
      AND b.id <> a.id
  );

-- name: SearchAuthors :many
SELECT /*%columns author_cols(a)*/ a.* /*%end*/
FROM authors a
WHERE /*%where visible_author(a)*/ TRUE /*%end*/
  AND /*%where name_filter(a)*/ TRUE /*%end*/;

-- name: ListAuthorsEmbed :many
SELECT /*%columns author_embed(a, o)*/ sqlc.embed(a), sqlc.embed(o) /*%end*/
FROM authors a
/*%joins author_org_left(a, o)*/ LEFT JOIN orgs o ON o.id = a.org_id /*%end*/
WHERE /*%where visible_author(a)*/ TRUE /*%end*/;
