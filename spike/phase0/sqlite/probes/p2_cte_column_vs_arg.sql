-- name: ListBigSpendersP2 :many
WITH recent_orders AS (SELECT o.* FROM orders o WHERE o.created_at > sqlc.narg(since))
SELECT a.id, a.name, sum(r.amount) AS total
FROM authors a
JOIN recent_orders r ON r.author_id = a.id
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND r.amount > sqlc.arg(min_amount)
GROUP BY a.id;
