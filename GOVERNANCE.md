<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# Governance

Sluice is a small, maintainer-led project. This document describes how decisions get made while we're pre-1.0; it will grow once there is a community to govern.

## Maintainers

The current maintainer is:

- Sven Herrmann (`@thatscalaguy`) — project lead, release manager.

Additional maintainers will be added by lazy consensus among existing maintainers before v1.0.0.

## Roles

**Maintainers** have write access, review incoming pull requests, cut releases, and are responsible for security response. Decisions that affect the project as a whole (architecture, license, dependency policy) require a maintainer sign-off.

**Contributors** are anyone who opens an issue or pull request. Sustained, high-quality contribution is the primary path to becoming a maintainer.

**Reviewers** (post-v1.0) are trusted contributors granted merge-after-approval authority in specific subsystems. They are proposed by a maintainer and confirmed by lazy consensus.

## Decision making

- **Lazy consensus.** Most decisions happen in pull requests and GitHub Discussions. A proposal is accepted if no maintainer objects within five business days. Objections must state the concern and suggest a path forward.
- **RFCs.** Non-trivial changes (new transports, new policy kinds, wire-format changes, license/dependency shifts) require an RFC under `docs/rfcs/`.
- **Tie-break.** If consensus cannot be reached, maintainers vote; simple majority wins, project lead breaks ties.

## Release cadence

Until v1.0.0, we cut a minor release when a coherent slice of the MVP plan lands and all CI lanes are green. Patch releases go out as needed. Release notes live in `CHANGELOG.md`; the release manager is responsible for running `goreleaser` and verifying artifacts (signatures, SBOM, multi-arch images).

Post-v1.0 we intend to move to a quarterly minor cadence with LTS branches, but will not commit to that until the project has real users.

## Conflicts of interest

Maintainers disclose material conflicts of interest (employment at a competitor, commercial support around Sluice, etc.) in `MAINTAINERS.md` once it exists. A maintainer with a conflict recuses from any decision where the conflict could bias the outcome.

## Code of conduct

Governance operates under [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Enforcement reports are handled by maintainers not named in the report.

## Amendments

This document is amended by pull request. Changes require sign-off from all current maintainers.
