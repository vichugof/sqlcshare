-- Fragment definitions (design draft). Never listed in sqlc.yaml.
-- Header shape mirrors sqlc's "-- name:" line: -- fragment: <name>(<identifier args>) :<kind>
-- Body runs to the next ';'. {a} tokens are identifier substitutions.

-- fragment: author_cols(a) :columns
{a}.id, {a}.name, {a}.org_id, {a}.created_at;

-- fragment: author_embed(a, o) :columns
sqlc.embed({a}), sqlc.embed({o});

-- fragment: visible_author(a) :where
{a}.deleted_at IS NULL AND {a}.org_id = sqlc.arg(org_id);

-- fragment: name_filter(a) :where
{a}.name = sqlc.narg(name) OR sqlc.narg(name) IS NULL;

-- fragment: author_org(a, o) :joins
JOIN orgs {o} ON {o}.id = {a}.org_id;

-- fragment: author_org_left(a, o) :joins
LEFT JOIN orgs {o} ON {o}.id = {a}.org_id;

-- fragment: recent_orders :cte
SELECT o.* FROM orders o WHERE o.created_at > sqlc.narg(since)::timestamptz;
