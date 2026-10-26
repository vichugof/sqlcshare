# biblebrain_lmp pilot: results

Date: 2026-10-25. Status: Phases 0 to 4 of `plan.md` done. The work is on
branch `experiment/sqlcshare-fragments` in the worktree
`~/public_html/fullstacklabs/FCBH/biblebrain_lmp-sqlcshare` (7 commits on top
of `develop` at 7304b90), not pushed, no PR. The user's `develop` tree was not
touched. sqlcshare is at v0.1.1 (commit 3e682ed, tag local only).

## Summary

- 19 fragments in `backend/fragments/` (3 files, 196 lines) replace the SQL
  that was written more than once. 41 of the 136 queries use at least one;
  `stock_no_tag` alone is used by 26 queries.
- Hand-written SQL went from 1,450 to 1,300 lines (sources 1,104 + fragments
  196, 10%); `language.sql` from 533 to 238. The plan estimated 190 to 210
  lines; the gap is explained under M1.
- The business rules are now written once: `'stock_no'` 32 sites to 5,
  `'pseudonym'` 16 to 3, `hash_id IS NULL` 7 to 1, `priority = 9` 18 to 5,
  `priority = 0` 15 to 4, `8012` 13 to 2, `6414` 34 to 18.
- No business logic changed. I1: every generated Go file is byte-identical to
  `develop` outside SQL constants and doc comments, at every commit. I2: 101
  SQL constants identical, 8 differ in whitespace only, 27 differ in text,
  every one in a reviewed category (keyword or alias case, parentheses from a
  `:where` splice, AND reorder). I3: build, vet, lint and `go test -race
  -short` give the baseline result at every commit. I4 was not run (no MySQL
  reachable).
- Cost: `make generate` takes 0.1 s; one new directory of fragments, the
  committed `gen/sql/`, 25 changed lines in `sqlc.yaml`, 7 in the Makefile,
  one README paragraph.
- Four library findings were fixed in sqlcshare v0.1.1 (aligned multi-line
  splices, `**` globs, a `stats` subcommand, clearer path errors) and the
  pilot was regenerated with it.

## Commits on `experiment/sqlcshare-fragments`

| commit | what | sources + fragments |
|---|---|---|
| 319cbf6 | Wire sqlcshare with identity expansion (no behaviour change) | +5 (fragments/README.md) |
| 3a51e96 | Group A: language name subselects (`lang_alt_name`, `lang_fcbh_name`, `lang_heart_name`, `lang_stocknumbers`, `lang_stocknumbers_unrestricted`) | +92 −235 |
| 8904db6 | Group B: tag joins (`stock_no_tag`, `pseudonym_tag`), `bible_stocknumber_joins`, `media_list_joins` | +71 −65 |
| 55a5847 | Group C: `stocknumber_pseudonym_cols` (8 copies, not the planned 5) | +14 −13 |
| 0252f3a | Group D: `not_restricted`, `english_active_licensor`, `licensor_logo_filter` | +37 −28 |
| 4f1e5be | Group E: per-search SELECT lists (`lang_rolv_cols`, `lang_iso_cols`, `lang_product_code_cols`, `lang_bible_cols`, `lang_name_cols`, `lang_alt_name_cols`) | +87 −110 |
| 53b6282 | Regenerate with sqlcshare v0.1.1 (aligned splices) and pin it | whitespace in gen/sql and sqlc/ only |

Each commit passed `gendiff.py` (I1, I2) and `go build`, `go vet` before it
was made, and the full I3 set afterwards.

## Invariants

### I1: generated Go API byte-identical

`gendiff.py` blanks every SQL constant and every full-line comment in
`backend/sqlc/*.go` and compares the 14 files with `develop`. PASS at all 7
commits: no struct, field, type, JSON tag, method signature or Scan order
changed, so no caller in `core/port` or the services can observe the
refactor. `sqlcshare.go` was not generated: no fragment binds a named
parameter, as the plan predicted.

### I2: SQL text

Final classification (`gendiff.py` on 53b6282): identical 101, whitespace-only
8, text-changed 27. Every text change falls in one of these categories; the
diff of each was read by hand.

| category | why it cannot change results | queries |
|---|---|---|
| keyword or alias case (11) | MySQL keywords and column aliases are case-insensitive; the changed aliases sit inside scalar subqueries (`AS name`, `as stocknumbers`) or are lower-cased by sqlc anyway (`Stocknumber`, I1 shows the same Go field). The `on` to `ON` sites are the lower-case joins of six queries. | findLanguageByFcbh, getLanguageByLanguageName, getLanguageByProductCode, getStocknumbersByLicenseGroup, getStocknumbersByLicenseGroups, findOneBibleFilesetWithLicenseGroupById, listHashIdByStocknumber, listLicenseGroupByStocknumber, getAccessGroupByStocknumber, getMediaByStockNumber, getMediaByStockNumberAndBible |
| parentheses only (11) | a `:where` splice is written as `(body)`; `AND (p)` and `WHERE (p AND q)` group an AND chain, which is a no-op | getLanguagesExcludeRestrictedByRolv, getLanguageExcludeRestrictedByIso, getLanguageExcludeRestrictedByBible, getLanguageExcludeRestrictedByAltLanguageName, getMediaListExcludeRestrictedByBibleID, getLicensorByName, getLicensorByLicenseGroup, getLicensorsByLicenseGroups, getLicensorLogo, updateLicensorLogo, deleteLicensorLogo |
| case + parentheses (2) | both of the above | getLanguageExcludeRestrictedByLanguageName, getLanguageExcludeRestrictedByProductCode |
| AND reorder (3) | `english_active_licensor` moves `deleted_at IS NULL` next to `language_id = 6414`, ahead of the organization id predicate; the conjuncts are side-effect-free comparisons and the single positional parameter keeps its place (I1 shows the same Params) | getLicensorById, getLicensorByIds, getStocknumberByLicensorId |

Full output with diffs: run `gendiff.py` in the worktree (see "Reproduce").

### I3: same build

| commit | build | vet | `go test -race -short ./...` | `golangci-lint run --config .golangci.yml` |
|---|---|---|---|---|
| baseline (develop, regenerated with sqlc v1.31.1, `git status` clean) | ok | ok | 3 failures (below) | 0 issues |
| 319cbf6 | ok | ok | no Go change | no Go change |
| 3a51e96, 8904db6, 55a5847, 0252f3a, 4f1e5be, 53b6282 | ok | ok | the same 3 failures | 0 issues |

The three failures exist on `develop` and are unrelated to SQL:
`TestConnectionV2` and `TestS3Operations` need a local S3 (they fail with
`dial tcp: missing address` and invalid AWS credentials), and
`TestStocknumberBoardColumnAllowed` expects a Monday board column list that
lacks `Pseudonym`. golangci-lint 2.11.1 was used locally (CI pins 2.12.2).

### I4: result sets

Not run: no `.env`, no MySQL in docker-compose, no `TEST_DATABASE_URL`. The
27 I2 exceptions rest on the category arguments above. If a dev database
becomes available, run old and new SQL for those 27 queries with the same
arguments and diff the rows; the SQL of both versions is in
`git show develop:backend/sqlc/<file>.sql.go` and the worktree.

## Metrics

### M1: hand-written SQL lines

| file | before | after |
|---|---|---|
| language.sql | 533 | 238 |
| bible_fileset.sql | 274 | 240 |
| licensor.sql | 178 | 164 |
| license_group.sql | 93 | 91 |
| stocknumber.sql | 83 | 82 |
| 7 other query files (unchanged lines: access_groups 35, batch 25, copyright 7, permission_pattern 63, bible 114, country 8, external_languages 37) | 289 | 289 |
| sources | 1,450 | 1,104 |
| fragments/language.sql, fileset.sql, licensor.sql | 0 | 196 |
| **total** | **1,450** | **1,300** |

Saved: 150 lines (10%). Cross-check: `gen/sql` holds 1,473 lines, 1,432
without the 41 provenance lines; the surplus over 1,450 is the parenthesised
`:where` splices and subselects that now occupy their own lines. The plan's
estimate of 190 to 210 was high because 85 of the 196 fragment lines
are comments and blank lines that document the rules, the one-line tag joins
keep their join keyword in the query and save nothing, and the two name-search
fragments of Group E could only cover the leading columns (the stock number
column differs between the siblings). `english_active_licensor` had 6 copies,
not 7 (the plan counted `ListLicensor`, whose predicate sits in a JOIN).

### M2: duplicated lines (`sqldup.py adapter [fragments]`)

| | before | after |
|---|---|---|
| non-trivial lines | 963 | 791 |
| lines that appear at least twice | 637 (66%) | 423 (53%) |
| distinct repeated lines | 157 | 132 |
| repeated lines that cross files | 317 | 162 |

What remains repeated is mostly structural (`FROM bible_filesets bf`, the
bibles/connections joins written with different aliases, `GROUP BY` lists)
and the Phase 2 candidates below.

### M3: rule sites (grep over sources + fragments; comments count)

| literal | before | after | where it still appears |
|---|---|---|---|
| `'stock_no'` | 32 | 6 | `stock_no_tag`, the two `lang_stocknumbers` bodies, `GetLanguageByProductCode` (predicate in WHERE), `MigrateStocknumberTags`, one comment |
| `'pseudonym'` | 16 | 4 | `pseudonym_tag`, `CreatePseudonym`, `DeletePseudonymByMediaIds`, one comment |
| `hash_id IS NULL` | 7 | 1 | `not_restricted` |
| `priority = 9` | 18 | 5 | `lang_fcbh_name`, `lang_heart_name`, `FindLanguageByFcbh`, the `GetLanguageByLanguageName` pair |
| `priority = 0` | 15 | 4 | `lang_alt_name`, `GetLanguageWithTranslationById`, the `GetLanguageByAltLanguageName` pair |
| `8012` | 13 | 3 | `lang_alt_name`, `GetLanguageWithTranslationById`, one comment |
| `6414` | 34 | 18 | `lang_fcbh_name`, the licensor rules, and 14 sites in bible/translation joins that no fragment covers |
| `deleted_at IS NULL` | 39 | 31 | an `active(x)` fragment would make it 1 (Phase 2) |

### M4: reuse (`sqlcshare stats`, v0.1.1)

```
gen/sql (mysql)
  sources      12 files   1104 lines
  fragments     3 files    196 lines   19 definitions
  expanded     12 files   1473 lines
  statements  136, 41 using a fragment (30%), 0 derived
  fragment                             kind     direct nested statements
  stock_no_tag                         sql          18      8         26
  pseudonym_tag                        sql          10      4         14
  lang_alt_name                        columns       2     10         12
  lang_fcbh_name                       columns       4      8         12
  stocknumber_pseudonym_cols           columns       8      0          8
  not_restricted                       where         5      2          7
  bible_stocknumber_joins              joins         6      0          6
  english_active_licensor              where         6      0          6
  lang_heart_name                      columns       3      0          3
  licensor_logo_filter                 where         3      0          3
  lang_alt_name_cols, lang_bible_cols, lang_iso_cols, lang_name_cols,
  lang_product_code_cols, lang_rolv_cols, lang_stocknumbers,
  lang_stocknumbers_unrestricted, media_list_joins      2      0          2
```

"nested" counts uses inside another fragment (`stock_no_tag` inside
`bible_stocknumber_joins` and `media_list_joins`, the name subselects inside
the Group E column lists). Provenance comments list direct uses only.

### M6: cost

- `make generate` (expansion, validation with marino, sqlc): 0.08 to 0.11 s.
- New: `fragments/` (README.md + 3 files), `gen/sql/` (12 files + manifest,
  committed because CI does not regenerate). Changed: `sqlc.yaml` (12 query
  entries become `queries: gen/sql` plus a 12-line `options.sqlcshare` block
  with comments), Makefile (`generate` target, `-check` in `precommit`),
  backend README (tool versions, layout, one onboarding paragraph), root
  README (install and generate instructions).
- Workflow: `make generate` instead of `sqlc generate`; a bare `sqlc generate`
  still works but regenerates from whatever is in `gen/sql`, and `make
  precommit` catches a stale `gen/sql`. Editors see directives in the sources;
  the pilot used the one-line form without fallback text, so a source file is
  no longer parseable SQL on its own.
- Readability, `GetLanguagesExcludeRestrictedByRolv` (34 lines on develop):

```sql
-- name: GetLanguagesExcludeRestrictedByRolv :many
SELECT /*%columns lang_rolv_cols(l)*/
FROM languages l
LEFT JOIN bibles b ON l.id = b.language_id AND b.deleted_at IS NULL
/*%joins bible_stocknumber_joins(b)*/
LEFT JOIN /*%sql pseudonym_tag(bft, bft_pseud)*/
LEFT JOIN countries on l.country_id = countries.id
WHERE l.rolv_code LIKE ? ESCAPE '\\'
AND /*%where not_restricted(bft_pseud)*/
GROUP BY l.id, l.name, l.iso;
```

  The expanded statement in `gen/sql/language.sql` (and the Go constant) is
  the original query with the subselects indented under the SELECT list.

### M7: library feedback

| # | finding | status |
|---|---|---|
| 1 | A configured fragments directory that does not exist is an error (`error: stat fragments: no such file or directory`); an empty one is accepted. Git cannot commit an empty directory, so the identity commit needed a placeholder; `fragments/README.md` works because only `.sql` files are read. | Message now `fragments: fragments does not exist` (v0.1.1); README documents the placeholder. |
| 2 | Multi-line fragment bodies kept the fragment file's indentation, so expanded subselects sat at column 1 inside an indented SELECT list, in `gen/sql` and in the Go constants. | Fixed in v0.1.1: bodies are indented under their directive, nested directives included; each body line is its own source-map segment so diagnostics stay exact. Pilot regenerated (53b6282), whitespace-only. |
| 3 | The sources sit at two depths (`adapter/*/mysql/query`, `adapter/agreement/*/mysql/query`), so two globs were needed. | `**` segments in v0.1.1 (`adapter/**/query/*.sql`); the pilot config keeps the two globs, switching is optional. |
| 4 | Provenance comments list direct uses only, so counting reuse from them undercounts fragments used by other fragments (18 vs 26 statements for `stock_no_tag`). | `sqlcshare stats` in v0.1.1 counts direct and nested uses per fragment plus line counts (M1, M4 come from it). |
| 5 | The one-line tag joins are spelled `JOIN`, `INNER JOIN` and `LEFT JOIN` across the codebase. A `:joins` fragment would have needed three definitions or a text change; a `:sql` fragment holding only `bible_fileset_tags {t} ON ...` lets the query keep its keyword. | Pattern documented in the README. |
| 6 | 99 directive sites, 19 fragments, zero diagnostics from the expander or marino and no sqlc error at any step; the source maps were never exercised. | Nothing to fix. |
| 7 | `sqlcshare generate -check` verifies `gen/sql` and `sqlcshare.go` against the sources, not that `backend/sqlc` is current with `gen/sql` (nothing did before either). | Open. `sqlc diff` in `precommit`/CI, or a `-check` that runs sqlc into a temporary directory, would close it. |
| 8 | A `:where` splice is always parenthesised, which is what made 13 constants "text-changed" for a single predicate such as `bft_pseud.hash_id IS NULL`. | Kept: the parentheses are what keeps OR precedence safe; the cost is review noise only. |
| 9 | Comments between fragment definitions are fine; a comment between a header and its body would be spliced into every use. | Not hit; worth a sentence in the README. |

## Phase 2 candidates (change generated code or query text; need the team)

1. `GetMediaListExcludeRestrictedByBibleID` lacks `f.set_size_code` that its
   sibling has; sharing the list adds a Row field. Intentional?
2. `GetLanguageWithTranslationById` builds `alt_name` with separator `' | '`,
   the other 12 copies use `';'`.
3. `GetLanguageByProductCode` filters `bft.name = 'stock_no'` in WHERE; its
   sibling does it in the join (`stock_no_tag`). Moving it is equivalent for
   an inner join and would let the pair share `bible_stocknumber_joins`-style
   joins.
4. `GetProductCodeWithBible` writes `bfst.name="stock_no"` (double quotes, in
   WHERE); `ListLicensor` puts `language_id = 6414` in the JOIN;
   `MediaByLicensor` has no language predicate. Normalising lets the licensor
   and tag rules cover them.
5. `MediaByLicensor`, `MediaByLicenseGroup`, `MediaByPermissionPattern`: same
   shape, different columns (`bft.mode_id` vs `bf.mode_id`, `bible_id`,
   `stocknumber`); unifying changes three structs and their callers.
6. Alias spelling (`alt_name`, `altName`, `AltName`) gives different Go
   fields; normalising touches repository code and JSON output.
7. `active(x)` for the 31 remaining `deleted_at IS NULL` sites, and a `6414`
   English-translation rule for the bible/organization joins (14 sites).
8. `FindLanguageByFcbh` computes `fcbh_name` through a derived table instead
   of `lang_fcbh_name`; same result, different plan.

## Open questions, as decided

1. I4: no database, not run (I1 to I3 only).
2. Locations: `backend/fragments`, `backend/gen/sql`, both committed.
3. Phase 2: nothing applied.
4. PR or branch: branch only, not pushed. A PR to `develop` additionally
   needs: the v0.1.1 tag pushed to GitHub (the READMEs pin
   `sqlcshare@v0.1.1`; pin v0.1.0 and drop 53b6282 otherwise), a CI step
   that installs sqlc v1.31.1 and sqlcshare and runs `sqlcshare generate
   -check` (note `backend-ci.yml` triggers on PRs to `main`, not `develop`),
   `docs/DEV_ENVIRONMENT.md` and the gitignored local `CLAUDE.md` updated
   from `sqlc generate` to `make generate`.

## Reproduce

```sh
S=$(mktemp -d); GOBIN=$S/bin go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
(cd ~/public_html/owner/sqlcshare && go build -o $S/bin/sqlcshare ./cmd/sqlcshare)
export PATH=$S/bin:$PATH
cd ~/public_html/fullstacklabs/FCBH/biblebrain_lmp-sqlcshare/backend
make generate && sqlcshare generate -check && sqlcshare stats
python3 -I "~/public_html/owner/sqlcshare/reports/biblebrain_lmp pilot/gendiff.py"   # I1, I2 vs develop
"~/public_html/owner/sqlcshare/reports/biblebrain_lmp pilot/metrics.sh" .            # M1 to M4
go build ./... && go vet ./... && go test -race -short ./... ; golangci-lint run --config .golangci.yml
```

`gendiff.py -b <ref>` compares against another baseline; `metrics.sh` on the
`develop` checkout prints the "before" column.
