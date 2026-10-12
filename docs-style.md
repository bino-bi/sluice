# Docs style guide

These rules apply to every page under `docs/`. This file is not published.

The goal: a reader finds a feature, sees a working example, and understands it in under a minute. Many readers are not native English speakers.

## 1. Document only what the code does today

- Check every feature, field, flag, default, metric, error code, MCP tool and endpoint against the code.
- Keep it only if it is implemented and a user can reach it.
- Do not write about planned or partial features. Do not write "planned", "coming soon" or "not yet implemented".
- If docs and code disagree, the code wins. Fix the docs.
- Leave out internals a user cannot act on: package paths, parser edge cases, cache keys, design history.
- If a user can hit a limit, write one short line under "Limits" or in the error-code reference.
- Reference pages are tables. Generate them from code where a generator exists (`make docs-generate`).

## 2. Plain, simple English

- One idea per sentence. Aim for 8 to 15 words. The hard limit is 25 words.
- Use active voice and present tense. Talk to the reader as "you".
- Use common words. Explain a technical term once, at first use, or link to the [glossary](docs/reference/glossary.md).
- Use one term per concept everywhere. The glossary lists the terms.
- Delete every sentence that does not help the reader do something.
- No em dashes and no en dashes in prose. Write "8 to 15", not a dash range.
- No semicolon chains. No nested parentheses.
- No marketing. No feature counts such as "11 policy kinds". Counts go stale.
- Use tables for fields and options. Use lists for three or more parallel items.

Do not use these words and phrases:

> seamless, robust, powerful, leverage, ensure, comprehensive, effortless, out of the box, under the hood, on the wire, in flight, first-class, battle-tested, simply, just, easily, delve, crucial, moreover, "it's worth noting", "note that", "it is important to", "in summary"

## 3. Examples

- Every feature page starts with a working example.
- Example shape: goal (1 sentence), policy or config, command or query, real output, "What happened" (at most 3 sentences).
- Every example is a set of real files under `examples/<name>/`, with sample data.
- Pages include these files with `pymdownx.snippets` (`--8<--`). Never paste code by hand.
- A fragment is allowed only as a named snippet section of a tested file.
- Outputs come from real runs and are stored as golden files. Never write expected output by hand.

## 4. Diagrams

- Keep every diagram listed in `docs-audit/diagrams.md`.
- Do not delete, redraw or restyle a diagram. Change a label only if the code proves it wrong.
- You may add a diagram in the same style if it replaces a long paragraph.

## 5. Structure

The top-level sections are fixed. Do not remove or rename them:

Home, Getting started, Architecture, Policies, Operations, Security, Reference, Cookbook.

## 6. Page template

Use this for feature pages. Reference pages and the threat model may use tables instead.

```
# <Feature name>
<1-2 sentences: what it does and when you use it.>

## Example
<tested example: goal, files, run, output, what happened>

## Fields
<table: name | type | default | meaning>

## More examples
<tested variants>

## Limits
<only if the user must know. Max 5 bullets.>

## See also
<2-4 links>
```
