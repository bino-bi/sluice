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
    r"delv(e|es|ed|ing)", r"crucial(ly)?", r"moreover", r"it(['’]s| is) worth noting", r"note that",
    r"it is important to", r"in summary",
    r"not yet implemented", r"not implemented yet", r"not supported yet", r"not yet supported",
    r"coming soon", r"planned", r"roadmap", r"future release", r"todo",
]
DASH_RE = re.compile(r"[—–‒―]|&(mdash|ndash|#8212|#8211|#x2014|#x2013);", re.I)
BANNED_RE = re.compile(r"(?<![\w-])(" + "|".join(BANNED) + r")(?![\w-])", re.I)
COUNT_RE = re.compile(
    r"\b(\d+|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve)\s+"
    r"(built-in\s+)?(policy kinds|kinds|mask types|mcp tools|tools|metrics|error codes|"
    r"data ?source types|drivers|engines|transports|recipes|endpoints|commands|providers|sinks)\b",
    re.I)
ABBREV_RE = re.compile(r"\b(e\.g|i\.e|etc|vs)\.", re.I)
HTML_CODE_RE = re.compile(r"<(pre|code|samp|tt|kbd|textarea)\b", re.I)
NESTED_PAREN_RE = re.compile(r"\([^()]*\([^()]*\)")
SENTENCE_END_RE = re.compile(r"(?<=[.!?])\s+")
FENCE_RE = re.compile(r"^(\s*)(```+|~~~+)\s*([\w-]*)")
SNIPPET_RE = re.compile(r'^\s*--8<-- "([^":]+)(?::([\w-]+))?"\s*$')
COMMENT_RE = re.compile(r"<!--.*?-->", re.S)
LIST_RE = re.compile(r"^\s*([-*+]|\d+\.)\s+")
CHECKED_SUFFIX = (".sh", ".json", ".yaml", ".yml", ".service")
# Manual files that pages may show. A new one needs a line here and in the final report.
MANUAL_FILES = {
    "examples/deployment/manual/docker-compose.yaml",
    "examples/deployment/manual/sluice.service",
    "examples/install/manual/archive.sh",
    "examples/install/manual/build.sh",
    "examples/install/manual/docker-example.sh",
    "examples/install/manual/image.sh",
    "examples/install/manual/second-terminal.sh",
    "examples/install/manual/start-example.sh",
    "examples/mcp-agent/manual/claude-desktop.json",
}
SNIPPET_MARK_RE = re.compile(r"-+8<-+")
SKIPPED_DIRS = ("tests", "testdata")  # the policy loader does not read these
NAV = ["Home", "Getting started", "Architecture", "Policies", "Operations",
       "Security", "Reference", "Cookbook"]


def clean_inline(text: str) -> str:
    # A code span counts with all its words, but is not checked for banned words.
    text = re.sub(r"`([^`]*)`", lambda m: " " + re.sub(r"\S+", "CODE", m.group(1)) + " ", text)
    text = re.sub(r"!\[([^\]]*)\]\([^)]*\)", r"\1", text)
    text = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", text)
    text = re.sub(r"</?[a-zA-Z][^<>]*>", " ", text)
    text = ABBREV_RE.sub(r"\1", text)
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
        if not stripped:
            yield from flush()
            continue
        if stripped.startswith(("!!!", "???", "===")):
            yield from flush()
            title = re.search(r'"([^"]*)"', stripped)
            if title:
                yield no, title.group(1)
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
        if DASH_RE.search(raw):
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


def indent_of(line: str) -> int:
    return len(line.expandtabs(4)) - len(line.expandtabs(4).lstrip(" "))


def indented_code(line: str, prev_blank: bool, before: list[str]) -> bool:
    """True for a Markdown indented code block: after a blank line, 4 spaces deeper
    than a paragraph, or 8 deeper than the list item or admonition that holds it."""
    indent = indent_of(line)
    if not prev_blank or indent < 4 or FENCE_RE.match(line):
        return False
    parent = next((p for p in reversed(before) if indent_of(p) < indent), "")
    holder = LIST_RE.match(parent) or parent.lstrip().startswith(("!!!", "???", "==="))
    return indent - indent_of(parent) >= (8 if holder else 4)


def check_file(md: Path, errors, pinned):
    rel = md.relative_to(ROOT)
    text = COMMENT_RE.sub(lambda m: "\n" * m.group(0).count("\n"), md.read_text())
    prose, fence, block, block_start, lang = [], None, [], 0, ""
    prev_blank, before = True, []
    for no, line in enumerate(text.splitlines(), 1):
        line = re.sub(r"^(\s*>)+ ?", "", line) if line.lstrip().startswith(">") else line
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
        if SNIPPET_MARK_RE.search(line):
            errors.append(f"{rel}:{no}: snippet include outside a code block")
            continue
        if HTML_CODE_RE.search(re.sub(r"`[^`]*`", "", line)):
            errors.append(f"{rel}:{no}: HTML code element, use a snippet include")
        if indented_code(line, prev_blank, before):
            errors.append(f"{rel}:{no}: indented code block, use a snippet include")
        if line.strip():
            before.append(line)
            prev_blank = False
        else:
            prev_blank = True
        prose.append((no, line))
    if fence is not None:
        errors.append(f"{rel}:{block_start}: code block is not closed")
    check_prose(rel, paragraphs(prose), errors)
    if "compliance/" in text:
        errors.append(f"{rel}: links to the removed Compliance section")


def tested_file(path: str) -> str:
    """Return "" when the example harness runs, loads, compares or syntax-checks the file."""
    parts = Path(path).parts
    if len(parts) < 3 or parts[0] != "examples" or ".." in parts:
        return "is not under examples/<name>/"
    example, rel = ROOT / parts[0] / parts[1], "/".join(parts[2:])
    if not (ROOT / path).is_file():
        return "does not exist"
    steps = sorted((example / "steps").glob("*.sh"))
    suites = sorted((example / "tests").glob("*.y*ml"))
    if not steps and not suites:
        return "is in an example without tests/ or steps/"
    yaml = rel.endswith((".yaml", ".yml"))
    if re.fullmatch(r"steps/[^/]+\.sh", rel) or rel == "serve.sh":
        return ""
    if rel.startswith("tests/") and yaml and "/" not in rel[6:]:
        return ""
    if rel.startswith("policies.d/") and yaml:
        if any(part in SKIPPED_DIRS or part.startswith(".") for part in parts[3:-1]):
            return "is in a directory that the policy loader skips"
        return "" if suites or (example / "serve.sh").exists() or uses(example, "policies.d", whole=True) else \
            "is in a policies.d that nothing loads"
    if rel.startswith("expected/"):
        return "" if (example / "steps" / (Path(rel).stem + ".sh")).is_file() else "has no step that writes it"
    if rel.startswith("manual/") or rel == "docker-compose.yaml":
        if not rel.endswith(CHECKED_SUFFIX):
            return "is a manual file without a syntax check"
        return "" if path in MANUAL_FILES else "is a manual file that is not in MANUAL_FILES"
    # Any other file must be named in something that runs: by its path, or by its
    # directory as a whole argument (`sluice policy validate invalid`, `moduleDir: ./rego`).
    # A directory covers only the files directly in it that its command reads.
    if uses(example, rel):
        return ""
    parent = "/".join(parts[2:-1])
    server = example / "server.yaml"
    is_module_dir = server.is_file() and re.search(
        rf"moduleDir:\s*[\"']?(\./)?{re.escape(parent)}/?[\"']?\s*$", server.read_text(), re.M)
    wanted = (".rego",) if is_module_dir else (".yaml", ".yml")
    if parent and rel.endswith(wanted) and uses(example, parent, whole=True):
        return ""
    return "is not used by serve.sh, a step or server.yaml"


def uses(example: Path, rel: str, whole: bool = False) -> bool:
    """True when a line that is not a comment in serve.sh, a step or server.yaml names rel."""
    files = [example / "serve.sh", example / "server.yaml", *sorted((example / "steps").glob("*.sh"))]
    pattern = re.compile(rf"(?<![\w./-]){re.escape(rel)}(?![\w-])")
    for f in files:
        if not f.is_file():
            continue
        for line in f.read_text().splitlines():
            if line.lstrip().startswith("#"):
                continue
            line = line.replace("./", "")
            if whole:
                if rel in (tok.strip("\"',;").rstrip("/") for tok in line.split()):
                    return True
            elif pattern.search(line):
                return True
    return False


def check_block(rel, start, lang, block, errors, pinned):
    body = "\n".join(block).strip("\n")
    if body.strip() in pinned:
        if pinned[body.strip()] != lang:
            errors.append(f"{rel}:{start}: kept diagram needs the fence language {pinned[body.strip()]!r}")
        return
    if lang == "mermaid":
        errors.append(f"{rel}:{start}: mermaid block has no copy in docs-audit/diagrams/")
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
    """Every stored diagram must appear unchanged in docs/. Returns body -> fence language."""
    pinned = {}
    all_docs = "\n".join(p.read_text() for p in sorted((ROOT / "docs").rglob("*.md")))
    for f in sorted((ROOT / "docs-audit" / "diagrams").iterdir()):
        body = f.read_text().strip("\n")
        pinned[body.strip()] = "mermaid" if f.suffix == ".mmd" else ""
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
    if re.search(r"compliance", (ROOT / "mkdocs.yml").read_text(), re.I):
        errors.append("mkdocs.yml: names the removed Compliance section")
    for md in (ROOT / "docs").rglob("*"):
        if "compliance" in md.name.lower():
            errors.append(f"{md.relative_to(ROOT)}: the Compliance section was removed")


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
