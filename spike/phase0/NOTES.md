# Phase 0 spike: findings

Date: 2026-10-19. sqlc v1.30.0 (installed, cgo build) and v1.31.1 (release binary, CGO_ENABLED=0).
Go 1.26. Everything below is reproducible with `./run.sh <label> [sqlc-binary]`; raw outputs are in
`results/<label>/` and generated Go in `results/<label>/<engine>.out/`.

## 1. Exit criteria

| Criterion | Result |
|---|---|
| Hand-expanded corpus (7 queries, 7 fragments) generates on PostgreSQL, MySQL, SQLite | Pass on all three, both versions (`*.corpus.txt`, exit 0) |
| Generated Go compiles | Pass, except MySQL with `query_parameter_limit > 1` (see 2) |
| Diagnostics shape per engine captured | See 3 |
| MySQL repeated-name binding verified | See 2 |
| v1.30.0 vs v1.31.1 | Identical: every text result and every generated file differs only in the `// sqlc vX` header |

## 2. MySQL: a parameter name that repeats after expansion

sqlc emits one `?` per occurrence on MySQL (PostgreSQL reuses `$1`, SQLite reuses `?1`).

- Default `query_parameter_limit` (1, params struct): the struct dedupes the field and the call repeats it
  per placeholder: `QueryContext(ctx, listAuthorsWithPeers, arg.OrgID, arg.OrgID)` and
  `QueryContext(ctx, searchAuthors, arg.OrgID, arg.Name, arg.Name)`. Compiles and binds correctly.
- `query_parameter_limit: 10` (positional Go parameters): sqlc generates
  `func (q *Queries) ListAuthorsWithPeers(ctx context.Context, orgID int64, orgID int64)` and
  `SearchAuthors(ctx, orgID int64, name sql.NullString, name sql.NullString)`.
  `go build` fails with `orgID redeclared in this block`. This is sqlc issue #4191; it reproduces for
  `sqlc.arg` and `sqlc.narg` on both v1.30.0 and v1.31.1.
- PostgreSQL and SQLite dedupe in both modes and compile.

Consequence for the expander: on MySQL it must know the effective `query_parameter_limit` and either refuse
or warn when a name occurs more than once in an expanded statement and the limit is above 1. The expander
can compute this statically from the expanded text.

## 3. Diagnostics shape

Format on every engine: `<path relative to the config file>:<line>:<col>: <message>`, one line per error,
all files and all statements of a `sql` block reported in one run, preceded by a `# package <name>` line,
exit status 1. Errors in several files of one `queries:` directory are all reported (`*.diag_all.txt`).

| Case | PostgreSQL | MySQL | SQLite |
|---|---|---|---|
| d4: bad column compared to a param, one-line query | `2:92: column "org_idd" does not exist` | same | same |
| d6: bad column after a 5-line CTE | `11:7: column "nam" does not exist` | same | same |
| d7: two bad queries in one file | both reported (`2:14`, `5:42`) | same | same |
| d5/d5b: unexpanded `sqlc.fragment(...)` | `6:1: function "sqlc.fragment" does not exist` (line of the `-- name:` header of that statement, col 1) | same | same |
| d3: missing `)` inside a fragment | `6:0: syntax error at or near ";"` (file has 5 lines; one line past) | `5:27: syntax error near ";"` (`;` is col 26) | two raw ANTLR lines on stderr first (`line 5:11 no viable alternative at input '='`, 0-based col), then `1:1: mismatched input ';' expecting {')', ','}` |

Column numbers are 1-based and point at the start of the column reference including its qualifier
(`a.org_idd` starts at col 92). Syntax-error positions are approximate and differ per engine; SQLite gives
no position in the structured line. Macro errors point at the statement header, not at the macro.

What sqlc does NOT report (important for fragment authors):

- d1: `WHERE (a.delted_at IS NULL AND ...)` passed on all three engines. A column reference in an `IS NULL`
  test is never resolved, so the typo reaches the database.
- d2: `o.creatd_at > sqlc.narg(since)::timestamptz` inside a CTE passed on PostgreSQL and MySQL (the
  parameter type comes from the cast, so the column is never resolved). SQLite, where the probe has no cast,
  caught it (`3:55: column "creatd_at" does not exist`).
- Rule of thumb observed: sqlc resolves a column only when it needs its type (output columns, and columns
  compared directly to a parameter). Everything else is checked only by `sqlc vet` with `sqlc/db-prepare`,
  `sqlc verify`, or tests against a real database.

Line numbers always refer to the file sqlc read (the expanded file), which confirms the need for a source
map in the runner.

## 4. Comments

- Full-line `-- sqlcshare: ...` provenance comment: stripped from the query text and emitted as a Go doc
  comment on the generated method (`// sqlcshare: queries/authors.sql:5 (cte recent_orders, ...)`).
- Full-line block comment `/* sqlcshare:marker-at-column-zero */` on its own line: also stripped from the
  query text and turned into a doc comment line. A marker that must survive into the SQL constant has to be
  inline on a line with code.
- Inline `SELECT /* sqlcshare */ a.id, ...`: survives verbatim in the `const` SQL.
- `sqlc vet` with the CEL rule `!query.sql.contains('/* sqlcshare */')` runs without a database and flags
  the six queries without the marker on all engines, output shape `authors.sql: CountAuthors: sqlcshare-marker: <message>`.

## 5. Other probes

| Probe | PostgreSQL | MySQL | SQLite |
|---|---|---|---|
| p2: CTE column compared to `sqlc.arg` (`r.amount > sqlc.arg(min_amount)`) | `7:7: table alias "r" does not exist` (issue #3585) | same | same |
| p2b: same with a cast (`::bigint`, `CAST(... AS SIGNED)`, `CAST(... AS INTEGER)`) | pass, `MinAmount int64` | pass | pass |
| p1: `sqlc.slice(ids)` inside a `:where` fragment, after another param | codegen passes but emits `a.id IN ($2)` with `Ids []int64`; invalid at runtime on PostgreSQL, where `= ANY($1::bigint[])` is the correct form | pass, `/*SLICE:ids*/?` expanded at runtime | pass; binding order `[orgID, ids...]` matches `?1` then unnumbered `?` |
| p1b: slice BEFORE another param | as p1 | pass | codegen passes but emits `IN (/*SLICE:ids*/?) ... = ?2` and binds `[ids..., orgID]`; SQLite numbers the unnumbered `?`s 1..n so `?2` is the second id (issue #2452). Slice params must come last on SQLite |
| p3: `sqlc.arg(org_id)` compared to BIGINT and to TEXT in one query | passes silently, first type wins (`orgID int64`) | `error generating code: named param OrgID has incompatible types: string, int64` | passes silently, int64 |
| p4: `LIMIT sqlc.arg(page_size)` | pass | `6:11: syntax error near "sqlc.arg(page_size);"`; `LIMIT ?` works | pass |

## 6. Configuration and invocation

- `queries: gen/sql` (a build directory) and a top-level `options: { sqlcshare: {...} }` map are accepted by
  both versions; the map is the carrier for expander settings inside `sqlc.yaml`.
- The directive-form sources in `queries/` (block-comment directives with fallback text, including a `:cte`
  directive placed inside an existing `WITH` with a fallback body) parse and generate on all three engines
  (`*.sources.txt`, exit 0). Sources stay sqlc-parseable even if the expander is not run.
- Timing of one `sqlc generate` for the PostgreSQL corpus: 0.02 s with the cgo build (v1.30.0 via
  `go install`), 1.03 s with the CGO_ENABLED=0 release binary (WASM parser start-up). Whole spike:
  2.2 s vs 23.5 s for 78 invocations. The runner should prefer a cgo build or batch work into one invocation.
- sqlc publishes no checksum asset for releases (asset list checked via the GitHub API); the v1.31.1 binary is
  verified only by its HTTPS origin.

## 7. Changes to the plan

1. Keep the `:cte` directive inside an existing `WITH ... AS (<fallback>)` rather than a `/*%with*/` prefix
   form; it keeps the source a complete statement and parsed cleanly on all engines.
2. Fragments on MySQL may use `?` for LIMIT/OFFSET (named macros are a syntax error there until sqlc's
   preprocess pass ships); the "named macros only" rule needs this exception.
3. The expander must statically flag, after expansion: repeated names on MySQL when
   `query_parameter_limit > 1` (compile failure), a parameter name used by two different fragments or by a
   fragment and the query (silent wrong type on PostgreSQL/SQLite), `sqlc.slice` in PostgreSQL fragments
   (runtime failure), and a slice parameter that is not last on SQLite (wrong binding).
4. Document the cast workaround for comparisons against CTE columns, and that `IS NULL` and cast-compared
   column references are not validated by sqlc; recommend `sqlc vet` with `sqlc/db-prepare` in CI.
5. The source map needs three mappings: column errors (line and 1-based column), statement-level errors
   (the `-- name:` header line), and file-level syntax errors (no usable position; fall back to the statement
   span or the whole source file).
6. The vet marker must be inline; the provenance line stays a full-line comment so it becomes a doc comment.

## 8. Still open

- Runtime behaviour was not executed against live databases (no servers available); the p1 PostgreSQL and
  p1b SQLite conclusions follow from the generated SQL and binding order, not from execution.
- sqlc `main` (unreleased) rewrites macros before parsing and swaps all parsers; rerun this spike on the next
  release.
