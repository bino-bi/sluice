#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Style check for docs/. The rules live in docs-style.md.

Prose: banned words, dashes, sentence length, semicolon chains, nested
parentheses, feature counts.
Code blocks: each block is a snippet include of an example file that the
example harness runs (scripts/docs-examples.py).
Structure: top-level nav, kept diagrams, no links to the removed Compliance section.
Example files: no em or en dashes, because the docs show them.

Usage: docs-style-check.py [path ...]   (default: docs/)
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MAX_WORDS = 25

BANNED = [
    r"seamless(ly)?", r"robust(ly|ness)?", r"powerful", r"leverag(e|es|ed|ing)",
    r"ensur(e|es|ed|ing)", r"comprehensive(ly)?", r"effortless(ly)?",
    r"out[- ]of[- ]the[- ]box", r"under the hood", r"on the wire", r"in[- ]flight",
    r"first[- ]class", r"battle[- ]tested", r"simply", r"just", r"easily",
    r"delve", r"crucial", r"moreover", r"it(['’]s| is) worth noting", r"note that",
    r"it is important to", r"in summary",
    r"not yet implemented", r"not implemented yet", r"coming soon", r"planned",
]
BANNED_RE = re.compile(r"(?<![\w-])(" + "|".join(BANNED) + r")(?![\w-])", re.I)
COUNT_RE = re.compile(
    r"\b(\d+|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve)\s+"
    r"(built-in\s+)?(policy kinds|mask types|mcp tools|tools|metrics|error codes|"
    r"data ?source types|drivers)\b", re.I)
NESTED_PAREN_RE = re.compile(r"\([^()]*\([^()]*\)")
SENTENCE_END_RE = re.compile(r"(?<=[.!?])\s+")
FENCE_RE = re.compile(r"^(\s*)(```+|~~~+)\s*([\w-]*)")
SNIPPET_RE = re.compile(r'^\s*--8<-- "([^":]+)(?::([\w-]+))?"\s*$')
COMMENT_RE = re.compile(r"<!--.*?-->", re.S)
LIST_RE = re.compile(r"^\s*([-*+]|\d+\.)\s+")
MANUAL_RE = re.compile(r"^(manual/.+|docker-compose\.yaml)$")
RUN_DIRS = ("policies.d/", "tests/", "steps/", "expected/")
NAV = ["Home", "Getting started", "Architecture", "Policies", "Operations",
       "Security", "Reference", "Cookbook"]


def clean_inline(text: str) -> str:
    text = re.sub(r"`[^`]*`", " CODE ", text)
    text = re.sub(r"!\[([^\]]*)\]\([^)]*\)", r"\1", text)
    text = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", text)
    text = re.sub(r"<[^>]+>", " ", text)
    return re.sub(r"[*_]{1,3}", "", text)


def paragraphs(lines):
    """Yield (line_no, text) units: paragraphs, list items, table cells, headings."""
    buf, start = [], 0

    def flush():
        nonlocal buf
        if buf:
            yield start, " ".join(buf)
        buf = []

    for no, line in lines:
        stripped = line.strip()
        if not stripped or stripped.startswith(("!!!", "???", "===")):
            yield from flush()
            continue
        stripped = re.sub(r"^>\s?", "", stripped)
        if stripped.startswith("#"):
            yield from flush()
            yield no, stripped.lstrip("#").strip()
        elif stripped.startswith("|"):
            yield from flush()
            for cell in stripped.strip("|").split("|"):
                if cell.strip() and not re.fullmatch(r"\s*:?-+:?\s*", cell):
                    yield no, cell.strip()
        elif LIST_RE.match(line):
            yield from flush()
            buf, start = [LIST_RE.sub("", line).strip()], no
        else:
            if not buf:
                start = no
            buf.append(stripped)
    yield from flush()


def check_prose(rel, units, errors):
    for no, raw in units:
        text = clean_inline(raw)
        for m in BANNED_RE.finditer(text):
            errors.append(f"{rel}:{no}: banned word or phrase: {m.group(0)!r}")
        if "—" in text or "–" in text:
            errors.append(f"{rel}:{no}: em or en dash in prose")
        if COUNT_RE.search(text):
            errors.append(f"{rel}:{no}: feature count: {COUNT_RE.search(text).group(0)!r}")
        if NESTED_PAREN_RE.search(text):
            errors.append(f"{rel}:{no}: nested parentheses")
        for sentence in SENTENCE_END_RE.split(text):
            words = [w for w in sentence.split() if re.search(r"\w", w)]
            if len(words) > MAX_WORDS:
                errors.append(f"{rel}:{no}: sentence has {len(words)} words (max {MAX_WORDS}): "
                              f"{' '.join(words[:8])} ...")
            if sentence.count(";") > 1:
                errors.append(f"{rel}:{no}: semicolon chain")


def check_file(md: Path, errors, pinned):
    rel = md.relative_to(ROOT)
    text = COMMENT_RE.sub(lambda m: "\n" * m.group(0).count("\n"), md.read_text())
    prose, fence, block, block_start, lang = [], None, [], 0, ""
    for no, line in enumerate(text.splitlines(), 1):
        m = FENCE_RE.match(line)
        if fence is None and m:
            fence, block, block_start, lang = m.group(2), [], no, m.group(3)
            continue
        if fence is not None:
            if line.strip().startswith(fence) and line.strip().strip("`~") == "":
                check_block(rel, block_start, lang, block, errors, pinned)
                fence = None
            else:
                block.append(line)
            continue
        if "--8<--" in line:
            errors.append(f"{rel}:{no}: snippet include outside a code block")
            continue
        prose.append((no, line))
    if fence is not None:
        errors.append(f"{rel}:{block_start}: code block is not closed")
    check_prose(rel, paragraphs(prose), errors)
    if "compliance/" in text:
        errors.append(f"{rel}: links to the removed Compliance section")


def tested_file(path: str) -> str:
    """Return "" when the example harness runs, loads or syntax-checks the file."""
    parts = Path(path).parts
    if len(parts) < 3 or parts[0] != "examples":
        return "is not under examples/<name>/"
    example, rel = ROOT / parts[0] / parts[1], "/".join(parts[2:])
    if not (ROOT / path).is_file():
        return "does not exist"
    if not ((example / "steps").is_dir() or (example / "tests").is_dir()):
        return "is in an example without tests/ or steps/"
    if rel.startswith(RUN_DIRS) or rel == "serve.sh" or MANUAL_RE.match(rel):
        return ""
    # Any other file must be named by something that runs.
    scripts = [example / "serve.sh", *sorted((example / "steps").glob("*.sh"))]
    ran = " ".join(f.read_text() for f in scripts if f.is_file())
    prefixes = ["/".join(parts[2:i]) for i in range(3, len(parts) + 1)]
    if any(re.search(rf"(?<![\w./-]){re.escape(pre)}(?![\w-])", ran) for pre in prefixes):
        return ""
    return "is not used by serve.sh or a step"


def check_block(rel, start, lang, block, errors, pinned):
    body = "\n".join(block).strip("\n")
    if lang == "mermaid" or body.strip() in pinned:
        return
    lines = [l for l in block if l.strip()]
    if not lines:
        errors.append(f"{rel}:{start}: empty code block")
    for line in lines:
        m = SNIPPET_RE.match(line)
        if not m:
            errors.append(f"{rel}:{start}: code block is not a snippet include of an example file")
            return
        path, part = m.group(1), m.group(2)
        problem = tested_file(path)
        if problem:
            errors.append(f"{rel}:{start}: {path} {problem}")
            continue
        text = (ROOT / path).read_text()
        if part == "2":
            if "SPDX-License-Identifier" not in text.split("\n", 1)[0]:
                errors.append(f"{rel}:{start}: {path}:2 is only for skipping an SPDX first line")
        elif part and part.isdigit():
            errors.append(f"{rel}:{start}: {path}:{part}: use a named section, not a line number")
        elif part and f"[start:{part}]" not in text:
            errors.append(f"{rel}:{start}: {path} has no section {part!r}")


def check_diagrams(errors):
    """Every stored diagram must appear unchanged in docs/."""
    pinned = set()
    all_docs = "\n".join(p.read_text() for p in sorted((ROOT / "docs").rglob("*.md")))
    for f in sorted((ROOT / "docs-audit" / "diagrams").iterdir()):
        body = f.read_text().strip("\n")
        pinned.add(body.strip())
        if body not in all_docs:
            errors.append(f"docs-audit/diagrams/{f.name}: diagram is missing or changed in docs/")
    return pinned


def check_examples(errors):
    """Example files are shown in the docs, so their comments follow the dash rule."""
    for f in sorted((ROOT / "examples").rglob("*")):
        if not f.is_file() or "expected" in f.parts or "data" in f.parts:
            continue
        try:
            text = f.read_text()
        except UnicodeDecodeError:
            continue
        for no, line in enumerate(text.splitlines(), 1):
            if "—" in line or "–" in line:
                errors.append(f"{f.relative_to(ROOT)}:{no}: em or en dash")


def check_nav(errors):
    nav, in_nav = [], False
    for line in (ROOT / "mkdocs.yml").read_text().splitlines():
        if line.startswith("nav:"):
            in_nav = True
        elif in_nav and line and not line.startswith(" "):
            in_nav = False
        elif in_nav and (m := re.match(r"^  - ([^:]+):", line)):
            nav.append(m.group(1).strip())
    if nav != NAV:
        errors.append(f"mkdocs.yml: top-level nav is {nav}, want {NAV}")


def main() -> int:
    errors: list[str] = []
    pinned = check_diagrams(errors)
    check_nav(errors)
    check_examples(errors)
    targets = [ROOT / a for a in sys.argv[1:]] or [ROOT / "docs"]
    for target in targets:
        for md in sorted(target.rglob("*.md")) if target.is_dir() else [target]:
            check_file(md.resolve(), errors, pinned)
    for e in errors:
        print(e)
    print(f"docs-style-check: {len(errors)} problem(s)", file=sys.stderr)
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
