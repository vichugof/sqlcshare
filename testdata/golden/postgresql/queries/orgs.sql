-- name: GetOrg :one
SELECT * FROM orgs WHERE id = sqlc.arg(id);

-- name: ListOrgs :many
SELECT * FROM orgs ORDER BY name;
