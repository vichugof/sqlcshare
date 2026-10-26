#!/usr/bin/env bash
# Usage: metrics.sh BACKEND_DIR
# Prints the pilot's M1 (lines), M2 (duplication), M3 (rule sites) and, once
# gen/sql exists, M4 (fragment reuse) for the hand-written SQL of
# biblebrain_lmp. Run it on the same backend before and after the refactor.
set -u
B=${1:?backend dir}
HERE=$(cd "$(dirname "$0")" && pwd)
cd "$B" || exit 1
SRC=$(ls adapter/*/mysql/query/*.sql adapter/agreement/*/mysql/query/*.sql 2>/dev/null)
FRAG=$(ls fragments/*.sql 2>/dev/null)
echo "## M1 hand-written SQL lines (sources + fragments)"
cat $SRC $FRAG | wc -l
echo "sources only: $(cat $SRC | wc -l); fragments: $( [ -n "$FRAG" ] && cat $FRAG | wc -l || echo 0)"
if [ -d gen/sql ]; then
  echo "gen/sql lines excluding provenance: $(cat gen/sql/*.sql | grep -vc '^-- sqlcshare:')"
fi
echo
echo "## M2 duplication (sqldup.py over adapter${FRAG:+ + fragments})"
python3 -I "$HERE/sqldup.py" adapter ${FRAG:+fragments} | head -3
echo
echo "## M3 rule sites (sources + fragments)"
for lit in "'stock_no'" "6414" "'pseudonym'" "8012" "deleted_at IS NULL" "hash_id IS NULL" "priority = 9" "priority = 0"; do
  n=$(cat $SRC $FRAG | grep -o -F -- "$lit" | wc -l)
  printf '%-22s %3d\n' "$lit" "$n"
done
if [ -d gen/sql ]; then
  echo
  echo "## M4 fragment reuse (from provenance lines in gen/sql)"
  echo "statements: $(cat gen/sql/*.sql | grep -c '^-- name:'); using a fragment: $(cat gen/sql/*.sql | grep -c '^-- sqlcshare:')"
  grep -h '^-- sqlcshare:' gen/sql/*.sql | sed 's/^[^(]*(//; s/)$//' | tr ',' '\n' | sed 's/^ *//' | sort | uniq -c | sort -rn
fi
