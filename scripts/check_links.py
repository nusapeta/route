#!/usr/bin/env python3
"""Fail when a Markdown file links to a missing relative path.

Checks inline links and reference definitions outside fenced code blocks.
External URLs, anchors, and templated paths are ignored.
"""

from __future__ import annotations

import pathlib
import re
import subprocess
import sys
from urllib.parse import unquote

ROOT = pathlib.Path(__file__).resolve().parent.parent
LINK = re.compile(r"\[[^\]]*\]\(([^)]+)\)")
REFDEF = re.compile(r"^\s*\[[^\]]+\]:\s*(\S+)")
FENCE = re.compile(r"^\s*(```|~~~)")
SCHEME = re.compile(r"^[a-zA-Z][a-zA-Z0-9+.-]*:")


def tracked_markdown() -> list[pathlib.Path]:
    try:
        out = subprocess.run(
            ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "*.md"],
            cwd=ROOT,
            check=True,
            capture_output=True,
        ).stdout
        return [ROOT / p for p in out.decode().split("\0") if p]
    except (subprocess.CalledProcessError, FileNotFoundError):
        return sorted(ROOT.rglob("*.md"))


def candidates(text: str) -> list[str]:
    found = []
    fenced = False
    for line in text.splitlines():
        if FENCE.match(line):
            fenced = not fenced
            continue
        if fenced:
            continue
        for target in LINK.findall(line):
            found.append(target)
        m = REFDEF.match(line)
        if m:
            found.append(m.group(1))
    return found


def local_path(target: str) -> str | None:
    target = target.strip()
    if target.startswith("<") and target.endswith(">"):
        target = target[1:-1]
    target = target.split("#", 1)[0].split("?", 1)[0]
    if not target or target.startswith("#") or SCHEME.match(target):
        return None
    if any(tok in target for tok in ("${", "{{", "${", "*")):
        return None
    return unquote(target)


def main() -> int:
    errors = []
    files = tracked_markdown()
    for md in files:
        base = md.parent
        for raw in candidates(md.read_text(encoding="utf-8", errors="replace")):
            target = local_path(raw)
            if target is None:
                continue
            if target.startswith("/"):
                dest = ROOT / target.lstrip("/")
            else:
                dest = base / target
            if not dest.exists():
                errors.append(f"{md.relative_to(ROOT)}: {raw}")
    if errors:
        print("broken relative links:", file=sys.stderr)
        for err in errors:
            print(f"  {err}", file=sys.stderr)
        return 1
    print(f"links ok: {len(files)} markdown files")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
