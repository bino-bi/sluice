#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Count prose words per docs section. Fenced code blocks, HTML comments
and snippet include lines are not counted. Prints a Markdown table."""

import re
import sys
from pathlib import Path

FENCE = re.compile(r"^\s*(```|~~~)")
COMMENT = re.compile(r"<!--.*?-->", re.S)
WORD = re.compile(r"\w")


def prose_words(text: str) -> int:
    text = COMMENT.sub("", text)
    count = 0
    in_fence = False
    for line in text.splitlines():
        if FENCE.match(line):
            in_fence = not in_fence
            continue
        if in_fence or "--8<--" in line:
            continue
        count += sum(1 for tok in line.split() if WORD.search(tok))
    return count


def main() -> int:
    docs = Path(sys.argv[1] if len(sys.argv) > 1 else "docs")
    sections: dict[str, tuple[int, int]] = {}
    for md in sorted(docs.rglob("*.md")):
        rel = md.relative_to(docs)
        section = rel.parts[0] if len(rel.parts) > 1 else "home"
        pages, words = sections.get(section, (0, 0))
        sections[section] = (pages + 1, words + prose_words(md.read_text()))
    print("| Section | Pages | Prose words |")
    print("|---|---:|---:|")
    for section, (pages, words) in sections.items():
        print(f"| {section} | {pages} | {words} |")
    total_pages = sum(p for p, _ in sections.values())
    total_words = sum(w for _, w in sections.values())
    print(f"| **total** | {total_pages} | {total_words} |")
    return 0


if __name__ == "__main__":
    sys.exit(main())
