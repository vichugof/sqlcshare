#!/usr/bin/env python3
"""gendiff.py: the gate for every commit of the sqlcshare pilot.

Usage: gendiff.py [-C BACKEND_DIR] [-b BASE_REF] [-v]

Compares the sqlc-generated Go package BACKEND_DIR/sqlc (default: the
current directory's sqlc/) with the same files at BASE_REF (default
develop, read with git show) and checks two invariants:

I1  With the SQL string constants and the full-line comments blanked, every
    generated file is byte-identical to the baseline: struct names, fields,
    types, JSON tags, method signatures and Scan order are unchanged, so
    every caller compiles and behaves the same.

I2  Each query constant is classified as IDENTICAL, WHITESPACE-ONLY (equal
    after dropping the "-- sqlcshare:" provenance line and collapsing
    whitespace) or TEXT-CHANGED. TEXT-CHANGED constants are printed with a
    diff and must be reviewed by hand; the pilot accepts only parentheses
    added by a :where splice and AND-predicate reorders, each listed by
    name in results.md. A constant that is TEXT-CHANGED only by parentheses
    is marked "(parentheses only)".

The exit status is 1 when I1 fails or a constant was added or removed.
TEXT-CHANGED constants never fail the gate by themselves.
"""
import argparse
import difflib
import os
import re
import subprocess
import sys

CONST_RE = re.compile(r"^const (\w+) = `(.*?)`$", re.M | re.S)
HEADER_LINES = 4  # "Code generated", "versions:", "sqlc vX", "source:"


def git_show(repo, ref, path):
    r = subprocess.run(["git", "-C", repo, "show", f"{ref}:{path}"],
                       capture_output=True, text=True)
    return r.stdout if r.returncode == 0 else None


def git_ls(repo, ref, path):
    r = subprocess.run(["git", "-C", repo, "ls-tree", "--name-only", f"{ref}:{path}"],
                       capture_output=True, text=True)
    return r.stdout.split() if r.returncode == 0 else []


def blank(text):
    """Blank SQL constants and drop full-line comments after the header."""
    text = CONST_RE.sub(lambda m: f"const {m.group(1)} = ``", text)
    out = []
    for i, line in enumerate(text.split("\n")):
        if i >= HEADER_LINES and line.lstrip().startswith("//"):
            continue
        out.append(line)
    return "\n".join(out)


def constants(text):
    return {m.group(1): m.group(2) for m in CONST_RE.finditer(text)}


def norm_lines(sql):
    """Provenance dropped, whitespace collapsed, blank lines removed."""
    out = []
    for line in sql.split("\n"):
        line = re.sub(r"\s+", " ", line).strip()
        if not line or line.startswith("-- sqlcshare:"):
            continue
        out.append(line)
    return out


def norm(sql):
    return " ".join(norm_lines(sql))


def fold(s):
    """Lower-case everything outside single- and double-quoted literals."""
    return re.sub(r"'[^']*'|\"[^\"]*\"|[^'\"]+",
                  lambda m: m.group(0) if m.group(0)[0] in "'\"" else m.group(0).lower(), s)


def strip_parens(s):
    return re.sub(r"[()]", "", s).replace(" ", "")


def classify(nb, nc):
    """Name the smallest equivalence that makes two normalised SQL texts equal.

    case only ......... SQL keyword or alias case (MySQL: keywords, column
                        names and aliases are case-insensitive; table names
                        are not, so the diff still has to be read)
    parentheses only .. grouping added by a :where splice
    reorder only ...... same tokens in a different order (an AND reorder)
    Combinations are joined with '+'. Anything else is 'unclassified'.
    """
    kinds = []
    b, c = nb, nc
    if fold(b) == fold(c):
        return "case only"
    if strip_parens(b) == strip_parens(c):
        return "parentheses only"
    if strip_parens(fold(b)) == strip_parens(fold(c)):
        return "case+parentheses only"
    tb, tc = fold(b).replace("(", " ").replace(")", " ").split(), fold(c).replace("(", " ").replace(")", " ").split()
    if sorted(tb) == sorted(tc):
        return "reorder only (same tokens, case/parentheses folded)"
    return "unclassified"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-C", dest="backend", default=".", help="backend directory holding sqlc/")
    ap.add_argument("-b", dest="base", default="develop", help="git ref of the baseline")
    ap.add_argument("-v", action="store_true", help="list every constant, not only the changed ones")
    a = ap.parse_args()

    backend = os.path.abspath(a.backend)
    repo = subprocess.run(["git", "-C", backend, "rev-parse", "--show-toplevel"],
                          capture_output=True, text=True, check=True).stdout.strip()
    rel = os.path.relpath(os.path.join(backend, "sqlc"), repo).replace(os.sep, "/")
    cur_dir = os.path.join(backend, "sqlc")

    base_files = set(git_ls(repo, a.base, rel))
    cur_files = {f for f in os.listdir(cur_dir) if f.endswith(".go")}
    ok = True

    # I1
    print(f"== I1: generated Go API vs {a.base} ({len(cur_files)} files)")
    for f in sorted(base_files | cur_files):
        if f not in base_files:
            level = "note" if f == "sqlcshare.go" else "FAIL"
            print(f"  {level}: {f} is new (not in baseline)")
            ok &= level == "note"
            continue
        if f not in cur_files:
            print(f"  FAIL: {f} removed")
            ok = False
            continue
        base = git_show(repo, a.base, f"{rel}/{f}")
        with open(os.path.join(cur_dir, f)) as fh:
            cur = fh.read()
        b, c = blank(base), blank(cur)
        if b == c:
            if a.v:
                print(f"  ok   {f}")
            continue
        ok = False
        print(f"  FAIL: {f} differs outside SQL constants and comments:")
        for line in difflib.unified_diff(b.split("\n"), c.split("\n"), f"{a.base}/{f}", f, lineterm="", n=2):
            print("    " + line)
    print("  I1 " + ("PASS" if ok else "FAIL"))

    # I2
    print(f"== I2: SQL constants")
    ident = ws = changed = 0
    changed_names = []
    for f in sorted(base_files & cur_files):
        base = git_show(repo, a.base, f"{rel}/{f}")
        with open(os.path.join(cur_dir, f)) as fh:
            cur = fh.read()
        bc, cc = constants(base), constants(cur)
        for name in sorted(bc.keys() | cc.keys()):
            if name not in cc:
                print(f"  FAIL: {f}: constant {name} removed")
                ok = False
                continue
            if name not in bc:
                print(f"  FAIL: {f}: constant {name} added")
                ok = False
                continue
            b, c = bc[name], cc[name]
            if b == c:
                ident += 1
                if a.v:
                    print(f"  IDENTICAL       {name}")
            elif norm(b) == norm(c):
                ws += 1
                if a.v:
                    print(f"  WHITESPACE-ONLY {name}")
            else:
                changed += 1
                tag = " (" + classify(norm(b), norm(c)) + ")"
                changed_names.append(name + tag)
                print(f"  TEXT-CHANGED    {name}{tag}  [{f}]")
                for line in difflib.unified_diff(norm_lines(b), norm_lines(c), a.base, "current", lineterm="", n=1):
                    if line.startswith(("---", "+++", "@@")):
                        continue
                    print("      " + line)
    print(f"  I2 summary: identical={ident} whitespace-only={ws} text-changed={changed}")
    if changed_names:
        print("  text-changed: " + ", ".join(changed_names))
    print("gendiff: " + ("PASS" if ok else "FAIL"))
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
