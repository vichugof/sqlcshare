# biblebrain_lmp pilot: sqlcshare on a real MySQL codebase

Date: 2026-10-25. Status: done, see results.md. When this was written nothing had been applied to biblebrain_lmp;
everything below marked "done" ran in the scratchpad on copies.

## Goal

Use biblebrain_lmp (`~/public_html/fullstacklabs/FCBH/biblebrain_lmp/backend`,
MySQL, sqlc) as the first real user of sqlcshare:

1. find the SQL that is written more than once and express it as fragments;
2. prove the refactor changes no business logic;
3. measure what it bought (lines removed, duplication, rule sites) and what it cost;
4. feed bugs and rough edges back into sqlcshare.

## What the survey found

| item | value |
|---|---|
| Engine / driver | MySQL, `database/sql`, go-sql-driver |
| sqlc config | `backend/sqlc.yaml`: 12 query files listed one by one, 4 schema dirs, Go package `sqlc` (committed, 5,645 lines) |
| sqlc versions | committed code header says v1.31.1; installed binary is v1.30.0; README and CLAUDE.md say v1.29.0 |
| Hand-written SQL | 12 files, 136 queries, 1,450 lines; `language.sql` alone is 533 lines / 20 queries |
| Count queries | none, so `-- derive:` has nothing to do here |
| Parameters | almost all `?`; `sqlc.slice` in 20 places, `sqlc.arg` in 3 |
| Tests | unit tests mock the repository ports; one integration test needs `TEST_DATABASE_URL`; no `.env` present locally; docker-compose has no MySQL |
| CI | `.github/workflows/backend-ci.yml`: golangci-lint, `go test -race -short`, `make build`; nothing regenerates sqlc output, generated code is committed |
| sqlc rows cross layers | `core/port/*/repository` interfaces return sqlc row types, so generated struct names, fields and JSON tags are part of the internal API |

Duplication baseline (`sqldup.py adapter`, script next to this file): 637 of
963 non-trivial lines (66%) appear at least twice; 317 of those repeat across
files. Rule literals in hand-written SQL: `'stock_no'` 32 sites, `6414` 34,
`'pseudonym'` 16, `8012` 13, `deleted_at IS NULL` 39, `hash_id IS NULL` 7,
`priority = 9` 18, `priority = 0` 15.

The duplication has three shapes:

- Correlated subselects for a language's alt name, FCBH name and heart name,
  copied into 12 queries across `language.sql` and `bible_fileset.sql`.
- Sibling pairs `GetXxxByY` / `GetXxxExcludeRestrictedByY` (6 pairs in
  `language.sql`, 1 in `bible_fileset.sql`): the same query plus one
  `LEFT JOIN bible_fileset_tags bft_pseud ... 'pseudonym'` and one
  `bft_pseud.hash_id IS NULL`.
- The stock number tag join and the stock number + pseudonym projection,
  repeated across 7 files.

Toolchain checks done in the scratchpad:

- sqlc v1.30.0 regenerates the committed code except the version header and
  three `id_2` temporaries that v1.31.1 names differently. The baseline must
  therefore be produced with sqlc v1.31.1 to be byte-identical.
- `sqlcshare expand -engine mysql` over the 12 files with no fragments
  reproduces every file byte for byte, so marino accepts all 136 statements
  (MATCH AGAINST, GROUP_CONCAT SEPARATOR, LIKE ESCAPE, `sqlc.slice`,
  `:execlastid`, trailing `-- TODO` after a semicolon).
- A spike with five real fragments (two subselects containing
  `SEPARATOR ';'`, a 4-line joins block, a parameterised pseudonym join, a
  `:where`) went through `sqlcshare generate` and sqlc; the Row struct and
  method signature of `GetLanguagesByRolv` are identical to the baseline. The
  `:where` splice comes out as `AND (bft_pseud.hash_id IS NULL)`. Fragment
  bodies keep their own indentation in the output (cosmetic).
- sqlcshare's own `go vet` and `go test ./...` pass at the current commit.

## What "no business logic change" means here

Three invariants, checked after every fragment group:

- **I1, generated Go API byte-identical.** Every file in `backend/sqlc`, with
  SQL string constants and doc comments blanked, equals the baseline. This
  covers struct names, field names, types, JSON tags, method signatures and
  Scan order, hence every caller.
- **I2, SQL text equivalent.** For each query constant, old and new differ
  only in whitespace, the `-- sqlcshare:` provenance line, parentheses that
  `:where` adds, or an AND-predicate reorder done on purpose. Every exception
  is listed by query name in the results.
- **I3, same build.** `go build`, `go vet`, `go test -race -short`,
  `golangci-lint run --config .golangci.yml` give the same result as the
  baseline run.

If a MySQL with data is reachable, add **I4**: run old and new SQL with the
same arguments and diff the result sets for every query on the I2 exception
list.

Out of scope for Phase 1: anything that changes a generated struct, or any Go
file other than what `sqlc.yaml`, the Makefile and docs need.

## Phase 0: baseline and harness (no behaviour change)

1. **Worktree.** `git -C biblebrain_lmp worktree add ../biblebrain_lmp-sqlcshare -b experiment/sqlcshare-fragments develop`.
   The user's `develop` tree and its untracked files stay untouched.
2. **Tools in the scratchpad, not globally.**
   `GOBIN=$S/bin go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1` and
   `go build -o $S/bin/sqlcshare ./cmd/sqlcshare` from this checkout, so a
   library fix can be rebuilt and re-run in seconds. Per biblebrain's
   CLAUDE.md rule, confirm in the sqlc 1.31 docs that a top-level `options:`
   key is ignored and that `queries:` accepts a directory.
3. **Reproduce.** `sqlc generate` with 1.31.1 in the worktree leaves
   `git status` clean. Record the I3 commands' output as the baseline.
4. **Baseline metrics** into `results.md` (the Phase 3 table, "before" column).
5. **Wire sqlcshare with zero fragments.**
   - `sqlc.yaml`: `queries: gen/sql`, plus
     `options.sqlcshare: {fragments: fragments, sources: [adapter/*/mysql/query/*.sql, adapter/agreement/*/mysql/query/*.sql], out: gen/sql}`.
     sqlcshare lists a directory one level deep like sqlc does, so use the two
     globs or the 12 explicit paths; there is no `**`.
   - `backend/fragments/` empty at first. Whether sqlcshare tolerates an
     empty or missing fragments directory is a pilot finding.
   - Makefile: `generate: sqlcshare generate -sqlc sqlc`; `precommit` gains
     `sqlcshare generate -check`. README: `make generate` replaces
     `sqlc generate`, and the version line is fixed.
   - Expect `gen/sql/*.sql` byte-identical to the sources and `backend/sqlc`
     unchanged. Commit: "Wire sqlcshare with identity expansion (no behaviour change)".
6. **Harness `gendiff.py`** (next to this plan). Reads the baseline with
   `git show develop:backend/sqlc/X.sql.go` and the current files, blanks SQL
   constants and doc comments, asserts I1; then compares each constant
   normalised (whitespace collapsed, provenance removed) and prints
   IDENTICAL / WHITESPACE-ONLY / TEXT-CHANGED with a diff for the last group.
   It is the gate for every commit in Phase 1.

## Phase 1: fragments that keep every generated struct identical

Work one group at a time: write the fragment, replace the copies with
directives, `make generate`, gendiff, build and test, commit. Each commit is
independently revertible. The alias always stays in the query
(`/*%columns lang_alt_name(l)*/ AS altName`), so JSON tags and field names
cannot move.

**Group A: language name subselects** (`fragments/language.sql`). Largest win, lowest risk.

| fragment | kind | copies | where | est. lines saved |
|---|---|---|---|---|
| `lang_alt_name(l)` | `:columns` | 12 | `language.sql` ×11 (not `GetLanguageWithTranslationById`, which uses `' \| '`), `ListMediaTypeByStocknumber` | ~55 |
| `lang_fcbh_name(l)` | `:columns` | 12 | `language.sql` ×11, `ListMediaTypeByStocknumber` | ~70 |
| `lang_heart_name(l)` | `:columns` | 3 | `GetLanguageWithTranslationById`, `FindLanguageByFcbh`, `ListMediaTypeByStocknumber` | ~7 |
| `lang_stocknumbers(l)`, `lang_stocknumbers_unrestricted(l)` | `:columns` | 2 + 2 | `GetLanguageBy(Alt)LanguageName` and their ExcludeRestricted siblings | ~11 |

`FindLanguageByFcbh` uses the bare table name: pass `languages` as the argument.

**Group B: join blocks** (`fragments/fileset.sql`).

| fragment | kind | copies | where | est. lines saved |
|---|---|---|---|---|
| `bible_stocknumber_joins(b)` | `:joins` | 6 | `language.sql` Rolv, Iso and Bible pairs; the `LEFT`/`INNER JOIN bibles` line stays in the query | ~7 |
| `media_list_joins(f)` | `:joins` | 2 | `GetMediaListByBibleID`, `GetMediaListExcludeRestrictedByBibleID` | ~4 |
| `pseudonym_tag(h, p)` | `:joins` | ~14 | 6 files | 0 (one-liner) |
| `stock_no_tag(h, t)`, `stock_no_tag_left(h, t)` | `:joins` | ~20 | 7 files | 0 |

**Group C: stock number + pseudonym projection, cross-file.**

| fragment | kind | copies | where | est. lines saved |
|---|---|---|---|---|
| `stocknumber_pseudonym_cols(t, p)` | `:columns` | 5 | `GetStocknumbersByLang`, `GetStocknumbersByBibleId`, `GetStocknumbersByLicenseGroup(s)`, `GetStocknumberByLicensorId` | ~2 |

`license_group.sql` writes `AS Stocknumber`; sqlc's MySQL parser lower-cases
aliases, so the generated field is `Stocknumber` either way. gendiff proves it.

**Group D: `:where` rules.**

| fragment | copies | body | est. lines saved |
|---|---|---|---|
| `not_restricted(p)` | 7 | `{p}.hash_id IS NULL` | 0 |
| `licensor_logo_filter` | 3 | `language_id = 6414 AND icon IS FALSE AND organization_id = ?` (a positional `?` inside a fragment exercises the MySQL-only rule) | ~2 |
| `english_active_licensor(o, t)` | 7 | `{t}.language_id = 6414 AND {o}.deleted_at IS NULL`; needs an AND reorder in 3 queries, listed as I2 exceptions | ~7 |

**Group E: per-pair SELECT lists.** Each `ByX` / `ExcludeRestrictedByX` pair
has an identical column list, so `lang_rolv_cols(l)`, `lang_iso_cols(l)`,
`lang_product_code_cols(l)`, `lang_bible_cols(l, b)`, `lang_name_cols(l, lt)`
and `lang_alt_name_cols(l, lt)` each replace two copies (~40 lines). Fragments
nest, so they use Group A; the 34-line
`GetLanguagesExcludeRestrictedByRolv` becomes about 10 lines.

Estimated Phase 1 total: 190 to 210 of 1,450 lines (about 14%);
`language.sql` from 533 to roughly 350. The line count understates the point:
`'pseudonym'` goes from 16 sites to 3, `hash_id IS NULL` 7 to 1, `8012` 13 to
2, `priority = 9/0` 33 to about 6, `'stock_no'` 32 to under 10.

Not exercised by this project, to be stated in the results: derived counts
(no Count queries) and shared `XxxParams` structs (fragments bind no named
parameters, so `sqlcshare.go` will be empty or absent). The golden corpus or
a second pilot covers those.

## Phase 2: candidates that change generated code (decide after Phase 1, with the team)

- `GetMediaListExcludeRestrictedByBibleID` lacks the `f.set_size_code` its
  sibling has. Sharing the column list adds a field to one Row struct. Is the
  omission intentional?
- `GetLanguageWithTranslationById` builds the alt name with separator `' | '`
  while the other 12 copies use `';'`. Intentional?
- `MediaByLicensor`, `MediaByLicenseGroup`, `MediaByPermissionPattern`: same
  shape, different columns (`bft.mode_id` vs `bf.mode_id`, `bible_id`,
  `stocknumber`). Unifying changes three structs and their callers.
- Alias spelling: `alt_name` vs `altName` give different Go fields
  (`AltName` vs `Altname`); normalising touches repository code.
- `active(x)` `:where` for the 39 `deleted_at IS NULL` sites: zero line
  savings, a readability judgement call.

## Phase 3: measurement

Run the same commands before (Phase 0.4) and after; both columns go into
`results.md`.

| metric | how |
|---|---|
| M1 hand-written SQL lines | `cat adapter/*/mysql/query/*.sql adapter/agreement/*/mysql/query/*.sql fragments/*.sql \| wc -l` (before: 1,450). Lines saved = before − after. Cross-check: lines in `gen/sql` minus provenance lines stays about 1,450. |
| M2 duplicated lines | `sqldup.py adapter fragments` (before: 637 of 963, 66%; cross-file 317) |
| M3 rule sites | grep counts of the literals above in sources plus fragments |
| M4 reuse | per fragment, uses counted from the provenance lines in `gen/sql` (`grep -c 'columns lang_alt_name'`); statements using at least one fragment out of 136 |
| M5 safety | gendiff summary (identical / whitespace-only / text-changed list), I3 results, I4 diff if it ran |
| M6 cost | `time make generate`; new files and config lines; the onboarding note; a readability sample (`GetLanguagesExcludeRestrictedByRolv` before and after) |
| M7 library feedback | every bug, confusing message or missing feature hit, with the fix commit in sqlcshare |

## Phase 4: decide and feed back

- Write `results.md`. Then either open a PR to `develop` for team review (CI
  needs `go install github.com/vichugof/sqlcshare/cmd/sqlcshare@vX` and
  `sqlcshare generate -check`), or keep the branch as evidence only.
- Fold findings into sqlcshare. Already visible candidates: re-indent
  multi-line splices to the directive's column; `**` globs or recursive
  sources; a `stats` subcommand printing M1 and M4, since generate already
  knows them; accept an empty fragments directory if it does not today.
- Tag v0.1.1 if fixes landed and update the biblebrain pin.

## Risks

- `:where` parentheses and AND reorders change SQL text. Covered by the I2
  review and by I4 when a database exists; semantics are unchanged for
  AND chains.
- Fragment indentation leaks into expanded SQL. Cosmetic, constants only.
- sqlc version drift (1.29 in docs, 1.30 installed, 1.31.1 committed).
  Pinned in Phase 0.2 and documented in the README change.
- Team workflow: editors now see directives in the sources, and a bare
  `sqlc generate` regenerates from `gen/sql` without expanding.
  `make generate` plus `-check` in precommit and CI prevent drift.
- golangci-lint is 2.11.1 locally while the docs say 2.1.6. Compare against
  the baseline run rather than against absolute cleanliness.

## Open questions (non-blocking; default in parentheses)

1. Is a dev or test MySQL reachable for I4? (assume no; I1 to I3 only, I4 reported as not run)
2. Fragment and output locations (`backend/fragments`, `backend/gen/sql`, both committed)
3. Phase 2 scope (none until the Phase 1 results are reviewed)
4. PR to `develop` or pilot branch only (decide at Phase 4)

## Effort

Phase 0 about 1 to 2 hours, Phase 1 about 3 to 5 hours for groups A to E,
Phase 3 about 1 hour, Phase 4 depends on the findings.
