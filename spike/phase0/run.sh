#!/usr/bin/env bash
# Phase 0 spike runner.
#   ./run.sh <label> [path-to-sqlc-binary]
# Runs, per engine: the hand-expanded corpus (must pass, struct and positional
# parameter modes), the directive-form sources (fallback semantics), the whole
# diag/ directory in one run, then every diag/ and probes/ file on its own in
# both parameter modes. Captures stdout+stderr+exit code under results/<label>/.
set -u
cd "$(dirname "$0")"
label="${1:?usage: run.sh <label> [sqlc-binary]}"
SQLC="${2:-sqlc}"
res="results/$label"
rm -rf "$res"; mkdir -p "$res"
"$SQLC" version > "$res/version.txt" 2>&1

run() { # run <outfile> <cmd...>
  local out="$1"; shift
  { "$@"; echo "exit=$?"; } > "$out" 2>&1
}

for engine in postgresql mysql sqlite; do
  pushd "$engine" >/dev/null
  rm -rf out
  run "../$res/$engine.corpus.txt"   "$SQLC" generate
  run "../$res/$engine.sources.txt"  "$SQLC" generate -f sqlc.sources.yaml
  run "../$res/$engine.diag_all.txt" "$SQLC" generate -f sqlc.diag.yaml
  run "../$res/$engine.vet.txt"      "$SQLC" vet -f sqlc.vet.yaml
  for f in diag/*.sql probes/*.sql; do
    name=$(basename "$f" .sql)
    for mode in struct positional; do
      limit=1; [ "$mode" = positional ] && limit=10
      sed -e "s#__QUERIES__#$f#" -e "s#__OUT__#out/single/${name}_$mode#" \
          -e "s#__PKG__#${name}_$mode#" -e "s#__LIMIT__#$limit#" \
          sqlc.single.tmpl.yaml > sqlc.single.yaml
      run "../$res/$engine.$name.$mode.txt" "$SQLC" generate -f sqlc.single.yaml
    done
  done
  rm -f sqlc.single.yaml
  rm -rf "../$res/$engine.out"; [ -d out ] && cp -r out "../$res/$engine.out"
  popd >/dev/null
done
echo "done: $res"
