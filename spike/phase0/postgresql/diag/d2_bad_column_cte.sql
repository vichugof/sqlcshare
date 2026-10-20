-- name: ListAuthorsD2 :many
-- sqlcshare: queries/authors.sql:5 (cte recent_orders, columns author_cols, where visible_author)
WITH recent_orders AS (SELECT o.* FROM orders o WHERE o.creatd_at > sqlc.narg(since)::timestamptz)
SELECT a.id, a.name, a.org_id, a.created_at, count(r.id) AS order_count
FROM authors a
LEFT JOIN recent_orders r ON r.author_id = a.id
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
GROUP BY a.id;
