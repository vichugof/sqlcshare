-- name: ListAuthorsD6 :many
WITH recent_orders AS (
  SELECT o.*
  FROM orders o
  WHERE o.created_at > CAST(sqlc.narg(since) AS DATETIME)
)
SELECT a.id, a.name, a.org_id, a.created_at, count(r.id) AS order_count
FROM authors a
LEFT JOIN recent_orders r ON r.author_id = a.id
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND a.nam LIKE sqlc.arg(pattern)
GROUP BY a.id;
