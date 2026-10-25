# sqlcshare

Reusable SQL fragments for [sqlc](https://sqlc.dev). Define a column list, a
WHERE predicate, a JOIN block or a CTE once, use it in many queries, and let
sqlc compile the result exactly as if you had written it out by hand.

sqlc compiles every `-- name:` query on its own and has no way to share SQL
text between queries. sqlcshare is the preprocessor step its maintainer
suggested instead of new `sqlc.*` syntax: it expands fragment directives into
plain `.sql` files that `sqlc.yaml` lists under `queries`. sqlc never sees a
directive, types each expanded query at its use site, and the expanded SQL
becomes a Go constant with zero runtime cost.

## Quick start

`fragments/authors.sql` (never listed in `sqlc.yaml`):

```sql
-- fragment: author_cols(a) :columns
{a}.id, {a}.name, {a}.org_id, {a}.created_at;

-- fragment: visible_author(a) :where
{a}.deleted_at IS NULL AND {a}.org_id = sqlc.arg(org_id);

-- fragment: recent_orders :cte
SELECT o.* FROM orders o WHERE o.created_at > sqlc.narg(since)::timestamptz;
```

`queries/authors.sql` (the source; also never listed in `sqlc.yaml`):

```sql
-- name: ListAuthors :many
WITH /*%cte recent_orders*/ recent_orders AS (SELECT o.* FROM orders o WHERE FALSE) /*%end*/
SELECT /*%columns author_cols(a)*/ a.* /*%end*/, count(r.id) AS order_count
FROM authors a
LEFT JOIN recent_orders r ON r.author_id = a.id
WHERE /*%where visible_author(a)*/ TRUE /*%end*/
GROUP BY a.id;

-- name: SearchAuthors :many
-- derive: CountSearchAuthors :count
SELECT /*%columns author_cols(a)*/ a.* /*%end*/
FROM authors a
WHERE /*%where visible_author(a)*/ TRUE /*%end*/
  AND a.name ILIKE sqlc.arg(pattern)
ORDER BY a.name
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);
```

`sqlc.yaml`:

```yaml
version: "2"
options:
  sqlcshare:
    fragments: fragments
    sources: queries
    out: gen/sql
sql:
  - engine: postgresql
    schema: schema
    queries: gen/sql        # sqlc reads the expanded files only
    gen:
      go:
        package: db
        out: db
```

Run `sqlcshare generate`. It writes `gen/sql/authors.sql`, which holds the
expanded queries and the count query derived from `SearchAuthors`:

```sql
-- name: SearchAuthors :many
-- sqlcshare: queries/authors.sql:9 (columns author_cols, where visible_author)
SELECT a.id, a.name, a.org_id, a.created_at
FROM authors a
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND a.name ILIKE sqlc.arg(pattern)
ORDER BY a.name
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountSearchAuthors :one
-- sqlcshare: queries/authors.sql:10 (count of SearchAuthors, where visible_author)
SELECT count(*) FROM authors a
WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))
  AND a.name ILIKE sqlc.arg(pattern);
```

then runs `sqlc generate` with the same configuration file, and finally
writes `db/sqlcshare.go` next to sqlc's output:

```go
// VisibleAuthorParams holds the parameters that fragment visible_author (fragments/authors.sql:4:1) binds.
// It is shared by CountSearchAuthors, ListAuthors and SearchAuthors.
type VisibleAuthorParams struct {
	OrgID int64
}

func (p SearchAuthorsParams) VisibleAuthorParams() VisibleAuthorParams
func (p SearchAuthorsParams) WithVisibleAuthorParams(f VisibleAuthorParams) SearchAuthorsParams

// CountSearchAuthorsParams returns the parameters of CountSearchAuthors, the count of SearchAuthors, taken from p.
func (p SearchAuthorsParams) CountSearchAuthorsParams() CountSearchAuthorsParams
```

so a paginated handler is

```go
rows, err := q.SearchAuthors(ctx, p)
total, err := q.CountSearchAuthors(ctx, p.CountSearchAuthorsParams())
```

The `-- sqlcshare:` line becomes the Go doc comment of the generated method,
and any sqlc error inside a fragment is reported at the fragment file and
line.

## Syntax

A fragment file holds definitions, each a header comment followed by a body
that ends at the next semicolon:

```sql
-- fragment: <name>(<params>) :<kind>
<body>;
```

Parameters are identifiers substituted for `{param}` tokens in the body
(outside string literals). Inside a body, `@name` is rewritten to
`sqlc.arg(name)` so the fragment also works on MySQL, where `@name` is a user
variable. Fragments may use other fragments; cycles are reported.

| Kind       | Spliced as            | Use it for                                  |
|------------|-----------------------|---------------------------------------------|
| `:columns` | verbatim              | a SELECT list or part of one                |
| `:where`   | `(body)`              | a predicate; parentheses keep OR precedence |
| `:joins`   | verbatim              | one or more JOIN clauses                    |
| `:cte`     | `name AS (body)`      | a CTE body, inside a WITH the query writes  |
| `:sql`     | verbatim              | anything else                               |

A query references a fragment with a block-comment directive:

```sql
/*%<kind> <name>(<args>)*/ fallback text /*%end*/
/*%<kind> <name>(<args>)*/
```

The first form replaces everything from the directive to `/*%end*/`; the
fallback text keeps the source a complete statement that editors, formatters
and sqlc itself can parse. The second form is replaced in place. A fallback
must be closed before the next directive, and cannot contain directives.

A `:sql` fragment can hold just the table and ON clause of a join, so that
every query keeps its own join keyword and one definition serves
`INNER JOIN /*%sql stock_no_tag(bf, bft)*/` as well as
`LEFT JOIN /*%sql stock_no_tag(f, bft_stock)*/`:

```sql
-- fragment: stock_no_tag(h, t) :sql
bible_fileset_tags {t} ON {t}.hash_id = {h}.hash_id AND {t}.name = 'stock_no';
```

### Derived count queries

A list query and its count query share everything but the SELECT list and
the paging clauses (sqlc issue #3410). A comment next to `-- name:` derives
the count:

```sql
-- name: ListAuthors :many
-- derive: CountAuthors :count
SELECT ... FROM authors a WHERE ... ORDER BY a.name LIMIT sqlc.arg(page_size);
```

The expander writes `CountAuthors` as a `:one` statement right after
`ListAuthors`: the SELECT list becomes `count(*)` and the ORDER BY, LIMIT,
OFFSET, FETCH and FOR clauses are dropped, parameters included, so the count
takes the filter parameters only. A statement that uses DISTINCT, GROUP BY,
HAVING, WINDOW or a set operation is counted through a derived table
instead, `SELECT count(*) FROM (...) AS sqlcshare_count`, so the count is the
number of rows the list returns; on MySQL that form needs the list's output
column names to be unique. The analysis sees through fragments: a `:columns`
fragment is dropped with the list, and a `:sql` fragment that holds nothing
but paging clauses is dropped with them. Anything the rewrite cannot do
safely, such as a fragment that mixes a WHERE with an ORDER BY, is an error
at the derive comment. The derive comment itself never reaches sqlc.

## What the output carries

- **Provenance.** Every statement that used a fragment gets a
  `-- sqlcshare: <source>:<line> (<kind> <name>, ...)` comment after its
  `-- name:` line. sqlc turns full-line comments into the Go doc comment of the
  generated method, so generated Go says which source and fragments produced
  each query.
- **Alignment.** The lines of a multi-line fragment body are indented so
  that they line up under the directive that spliced it, whatever the
  indentation of the fragment file, so the expanded SQL, and the Go constant
  sqlc makes of it, reads like hand-written SQL.
- **Marker (opt-in).** With `marker: true`, `/* sqlcshare */` is inserted after
  the first keyword of every statement. Inline comments survive into sqlc's
  query text, so this `sqlc vet` rule flags any query that bypassed the
  expander:

  ```yaml
  rules:
    - name: sqlcshare-marker
      message: "query did not come from sqlcshare"
      rule: "!query.sql.contains('/* sqlcshare */')"
  ```

- **Manifest.** `gen/sql/sqlcshare.manifest.json` lists the files sqlcshare
  wrote, so outputs whose source disappeared are removed on the next run and
  nothing else in the directory is ever touched. sqlc ignores the file.
- **Source maps.** `sqlcshare generate` rewrites sqlc's `file:line:col`
  diagnostics to the source or fragment position and collapses the copies sqlc
  reports for every query that uses the fragment, for example
  `fragments/authors.sql:8:28: column "org_idd" does not exist (in fragment
  visible_author, expanded at gen/sql/authors.sql:7:33 and 6 more)`.

## Validation before sqlc runs

`sqlcshare generate` parses every fragment and every expanded statement
before writing anything, with the parsers the sqlc project itself maintains:
[oliphant](https://github.com/sqlc-dev/oliphant) for PostgreSQL (the pure Go
port of libpg_query), [marino](https://github.com/sqlc-dev/marino) for MySQL
(the sqlc fork of the TiDB parser) and [meyer](https://github.com/sqlc-dev/meyer)
for SQLite. All three are pure Go; oliphant is an 18 MB download.

A fragment is checked when it is loaded, inside a template for its kind
(`SELECT <columns> FROM t`, `SELECT 1 FROM t WHERE (<where>)`,
`SELECT 1 FROM t <joins>`, `WITH <cte> SELECT 1`), with its own parameter
names as arguments; a `:sql` fragment is checked only where it is used. An
error is reported once, at the fragment file and column, instead of once per
query that uses the fragment and at sqlc's approximate position:

```
fragments/authors.sql:8:28: error: fragment "visible_author": syntax error: syntax error at or near "AND"
```

Expanded statements are checked next, and an error inside a splice names the
fragment it came from. What each engine accepts matches sqlc: PostgreSQL and
MySQL parse the macros as function calls, so `LIMIT sqlc.arg(n)` is rejected
on MySQL exactly as sqlc rejects it; on SQLite the macros are rewritten to
placeholders first, as sqlc's own preprocessor does, because SQLite has no
schema-qualified function calls. meyer follows SQLite's grammar exactly and is
stricter than the ANTLR grammar sqlc 1.31 uses, so it can reject SQL that sqlc
would accept; `-validate off` disables the check and `-validate sqlc` runs
the installed sqlc's own `sqlc parse` instead (sqlc 1.31 or later, no
positions, one process per statement).

The expander also warns when a directive sits where its kind cannot be right,
such as a where fragment directly after SELECT, and when a fragment parameter
appears inside a string literal or comment, where it is not substituted.

Library users get the same through `sqlcshare.Options.Checkers` (package
`check` provides `Builtin()`), `Expander.Checker` and
`sqlcshare.CheckFragments`; the core package has no parser dependency.

## Go side: shared parameter structs

After `sqlc generate`, `sqlcshare generate` writes `sqlcshare.go` into every
Go package sqlc produced from a sqlcshare output directory (package `gogen`
for library use; `sqlcshare gocode` runs just this step). The file holds:

- **A struct per fragment** that binds parameters, named after the fragment
  (`visible_author` gives `VisibleAuthorParams`), with one field per
  parameter named as sqlc names it, honouring `rename` and `initialisms`.
- **Conversion methods** on every `XxxParams` struct sqlc generated for a
  query that uses the fragment: `p.VisibleAuthorParams()` extracts the
  fragment's parameters and `p.WithVisibleAuthorParams(f)` returns `p` with
  them set, so one filter value feeds every query that shares the fragment.
- **A method per derived count**, `p.CountAuthorsParams()` on
  `ListAuthorsParams`, that builds the count's parameters from the list's.

Types are copied from sqlc's generated structs rather than mapped from
database types, so they are whatever sqlc chose under the overrides and
`sql_package` in effect, and the file compiles by construction. A fragment
whose parameter sqlc typed differently in two queries, or that binds
different parameter names in two uses (renamed `{p}` arguments), gets no
struct; `-v` prints why. A query sqlc gave no parameter struct, because it
has one parameter or `query_parameter_limit` allows bare ones, gets no
methods, since its arguments are passed directly. The file is removed when
there is nothing to generate and is never written over a file sqlcshare did
not generate.

## Command line

```
sqlcshare generate [-f sqlc.yaml] [-check] [-no-sqlc] [-no-go] [-sqlc "sqlc"] [-validate builtin|sqlc|off] [-v]
sqlcshare expand -engine ENGINE -sources PATHS -out DIR [-fragments PATHS] [-root DIR] [-marker] [-no-provenance] [-check] [-validate builtin|sqlc|off]
sqlcshare gocode [-f sqlc.yaml] [-check] [-v]
sqlcshare stats [-f sqlc.yaml]
sqlcshare version
```

`generate` reads `options.sqlcshare` from `sqlc.yaml`, or `sqlcshare.yaml`
next to it, runs `sqlc generate -f <file>`, then writes `sqlcshare.go` into
each Go package (`-no-go` skips that). The engine is inferred from the sql
block whose `queries` is the target's `out`; set `engine:` to override.
Several expansions go under `targets:`, each with the same keys. `-check`
writes nothing and exits 1 when any output, `sqlcshare.go` included, is out
of date, for CI.

`fragments` and `sources` are files, directories (listed one level deep, as
sqlc lists a queries directory) or globs, in which a `**` segment matches any
number of directories and a trailing `**` lists a directory tree, so
`adapter/**/query/*.sql` names every query directory of a layered tree. A
configured fragments directory must exist; an empty one, or one holding only
a README, is fine, since only `.sql` files are read.

`stats` prints, per target, how many lines the sources, fragments and
expanded files hold and, for each fragment, how many times it is used
directly in a statement, nested inside another fragment, and in how many
statements. It writes nothing and is meant for measuring a migration.

With Go 1.24 tool dependencies:

```
go get -tool github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
go get -tool github.com/vichugof/sqlcshare/cmd/sqlcshare
//go:generate go tool sqlcshare generate -sqlc "go tool sqlc"
```

Build sqlc with cgo where you can: the cgo build parses the PostgreSQL corpus
in about 20 ms, the WASM build in about a second per invocation.

## Rules that keep fragments correct on every engine

These come from running sqlc v1.30.0 and v1.31.1 over an expanded corpus on
all three engines (see `spike/phase0/NOTES.md`). sqlcshare enforces the first
four and reports the rest where it can.

- Fragments use named macros only: `sqlc.arg`, `sqlc.narg`, `sqlc.slice`,
  `sqlc.embed` or `@name`. `$1` is rejected everywhere and `?` on SQLite,
  because sqlc numbers named parameters after expansion but cannot renumber
  positional ones. On MySQL `?` is allowed, since `LIMIT sqlc.arg(n)` is a
  syntax error there and `LIMIT ?` is the only form.
- `sqlc.slice` is rejected in PostgreSQL fragments: sqlc compiles it to
  `IN ($n)` with a slice argument, which fails at runtime. Use
  `= ANY(sqlc.arg(ids)::bigint[])`.
- On SQLite a `sqlc.slice` parameter must be the last parameter of the
  statement, or the generated code binds the wrong indexes (sqlc #2452).
- On MySQL a parameter name that appears more than once in a statement, which
  fragment reuse makes common, is fine with sqlc's default
  `query_parameter_limit` but does not compile with a higher limit (sqlc #4191).
- Comparing a CTE column with `sqlc.arg` fails with `table alias does not
  exist` on every engine (sqlc #3585); cast the argument.
- sqlc resolves a column only when it needs its type. A typo in an `IS NULL`
  test or next to a cast parameter is not reported; run `sqlc vet` with
  `sqlc/db-prepare` against a database to catch those.
- A parameter name bound by two different fragments, or by a fragment and the
  query, gets the type of its first use on PostgreSQL and SQLite; only MySQL
  reports a conflict. sqlcshare reports such sharing with `-v`.

## Status

All four phases of the plan in `reports/` are implemented: the expander,
source maps, manifest-owned output, the command line, parser-backed
validation, derived count queries, and the Go-side shared parameter structs.
The golden tests compile and vet the expanded corpus with the `sqlc` on PATH
for every engine, type-check the generated `sqlcshare.go` against sqlc's
output, and the `check` package tests run the corpus through the built-in
parsers. Phase 4 reads sqlc's generated Go instead of being a sqlc codegen
plugin, because a plugin sees database types but not the Go types sqlc's own
generator picks, and matching those is what makes a shared struct compile.

The first real-world use is the biblebrain_lmp pilot (a MySQL backend with
136 queries in 1,450 lines; `reports/biblebrain_lmp pilot/results.md`): 17
fragments took 150 lines and most of the repeated business rules out of the
queries while every generated Go struct and method stayed byte-identical.

```
go test ./...            # add -update to rewrite testdata/golden/*/want
```
