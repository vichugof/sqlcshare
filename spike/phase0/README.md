# Phase 0 spike

Hand-expanded fragment corpus run through sqlc on PostgreSQL, MySQL and SQLite,
plus deliberately broken "expanded" files to capture the diagnostics shape, and
probes for behaviours the research could not confirm (MySQL repeated-name binding,
CTE column vs sqlc.arg, sqlc.slice inside a fragment, parameter type conflicts).

Layout per engine:

- `schema/`          DDL for the catalog
- `fragments/`       fragment definitions in the proposed `-- fragment:` syntax (not read by sqlc)
- `queries/`         directive-form sources with fallback text (read only by sqlc.sources.yaml)
- `gen/sql/`         what the expander would emit; this is what sqlc.yaml reads
- `diag/`            expanded files with one error each
- `probes/`          expanded files that may or may not compile
- `sqlc*.yaml`       one config per experiment; sqlc.single.tmpl.yaml is filled by run.sh

Run: `./run.sh v1.30.0` (uses `sqlc` on PATH) or `./run.sh v1.31.1 /path/to/sqlc`.
Results land in `results/<label>/`, generated Go is copied to `results/<label>/<engine>.out/`.
Findings are in NOTES.md. Add `d5b` style cases by dropping a file into diag/ or probes/ and rerunning.
