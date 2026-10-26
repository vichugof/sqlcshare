#!/usr/bin/env python3
"""Line-level duplication report for a set of hand-written sqlc query files.

Usage: sqldup.py FILE_OR_DIR [FILE_OR_DIR ...]

Counts the non-trivial SQL lines (after trimming, comment stripping,
whitespace collapsing and lower-casing) that occur more than once across the
given files. It is a coarse "how much of this SQL is written twice" metric,
meant to be run before and after a sqlcshare refactor on the same inputs
(sources plus fragments) so the two numbers are comparable.
"""
import collections
import os
import re
import sys

TRIVIAL = {";", ")", ") as", "from", "where", "select", "group by", "order by", "limit 1"}
MIN_LEN = 15


def norm(line):
    line = re.sub(r"--.*$", "", line.strip()).strip()
    return re.sub(r"\s+", " ", line).lower()


def sql_files(paths):
    for p in paths:
        if os.path.isdir(p):
            for root, _, names in os.walk(p):
                for n in sorted(names):
                    if n.endswith(".sql"):
                        yield os.path.join(root, n)
        else:
            yield p


def main(paths):
    files = sorted(set(sql_files(paths)))
    total = nontrivial = 0
    counts = collections.Counter()
    where = collections.defaultdict(set)
    per_file = {}
    for f in files:
        n = 0
        with open(f) as fh:
            for raw in fh:
                total += 1
                l = norm(raw)
                if l.startswith("-- ") or len(l) < MIN_LEN or l in TRIVIAL:
                    continue
                nontrivial += 1
                n += 1
                counts[l] += 1
                where[l].add(os.path.basename(f))
        per_file[f] = (sum(1 for _ in open(f)), n)
    dup = sum(c for c in counts.values() if c > 1)
    distinct = sum(1 for c in counts.values() if c > 1)
    cross = sum(c for l, c in counts.items() if c > 1 and len(where[l]) > 1)
    print(f"files={len(files)} total_lines={total} nontrivial={nontrivial}")
    print(f"duplicated nontrivial lines (appear >=2x): {dup}"
          f" ({dup / max(nontrivial, 1):.0%} of nontrivial, {dup / max(total, 1):.0%} of all)")
    print(f"distinct repeated lines: {distinct}; repeated lines that cross files: {cross}")
    print("\nper file (total lines, nontrivial):")
    for f, (t, n) in per_file.items():
        print(f"{t:6d} {n:5d}  {f}")
    print("\ntop repeated lines:")
    for l, c in counts.most_common(30):
        if c < 3:
            break
        print(f"{c:3d}x [{len(where[l])} files] {l[:110]}")


if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    main(sys.argv[1:])
