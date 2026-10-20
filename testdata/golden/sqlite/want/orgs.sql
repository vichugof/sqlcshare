-- name: GetOrg :one
SELECT /* sqlcshare */ * FROM orgs WHERE id = sqlc.arg(id);

-- name: ListOrgs :many
SELECT /* sqlcshare */ * FROM orgs ORDER BY name;
