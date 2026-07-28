# Contributing to sluice

Thank you for your interest in contributing. This document covers the ground rules.

## Developer Certificate of Origin (DCO)

All contributions must be signed off per the [Developer Certificate of Origin 1.1](https://developercertificate.org/). Every commit message must include a `Signed-off-by:` trailer matching the author:

```
Signed-off-by: Your Name <your.email@example.com>
```

Use `git commit -s` to add the trailer automatically.

## Licensing of contributions

By submitting a contribution, you agree that your work is licensed under the project's existing terms:

- Changes to files under `pkg/` or `sdk/` → **Apache-2.0**
- Changes elsewhere → **AGPL-3.0-or-later**

Every new source file must begin with the correct SPDX header. `scripts/check-spdx.sh` enforces this in CI.

## Development workflow

```bash
make fmt        # gofmt + goimports
make vet        # go vet
make lint       # golangci-lint run + SPDX check
make test       # go test -race -short ./...
make all        # everything above, then build
```

CI runs `make lint test` on Linux; the release pipeline cross-compiles.

## Coding conventions

- Exported Go identifiers: CamelCase. Packages: lowercase single word.
- YAML fields: lowerCamelCase. Env vars: `SLUICE_*`. CLI flags: kebab-case.
- Errors: sentinel per package; client-facing responses funnel through `pkg/errors.APIError`.
- `log/slog` for all structured logging. `fmt.Print*` is forbidden outside `cmd/sluice/` (enforced by `forbidigo`).
- Time: UTC, RFC 3339 for audit; `time.Now().UTC()` everywhere.
- Layering: `pkg/**` must never import `internal/**`. `cmd/sluice` is the only composition root. Enforced by `depguard`.

## Cutting a release

Releases are initiated from the GitHub UI, not from a local `git tag`. Publishing the release is what creates the tag and starts the pipeline.

1. **Prepare the changelog on `main`.** Rename the `## [Unreleased]` heading to `## [X.Y.Z] — YYYY-MM-DD`, open a fresh empty `## [Unreleased]` above it, and update the two link references at the bottom of the file.
2. **Bump the pinned image tag.** The example compose files and the docs that quote them pin an exact version, because pre-releases are never tagged `latest`. Find and update every occurrence:

   ```bash
   grep -rn "ghcr.io/bino-bi/sluice:[0-9]" examples/ docs/ README.md
   ```

   Merge both changes to `main` before drafting the release.
3. **Dry-run the pipeline locally** (optional but recommended for the first tag on a new matrix):

   ```bash
   make release-snapshot     # goreleaser --snapshot --clean --skip=publish
   ```

   This needs `goreleaser`, `docker buildx`, `cosign`, and `syft` on `PATH`, plus `LIBDUCKDB_DIR` pointing at a linux/amd64 static bundle.
4. **Draft the release.** Go to *Releases → Draft a new release*, type the new tag (`vX.Y.Z`, or `vX.Y.Z-rc.N` for a candidate) into the tag selector — choosing *Create new tag on publish* — and target `main`. Write the release notes in the **body**; the title is overwritten with the tag by goreleaser, so do not put prose there.
5. **Publish.** This creates the tag and triggers `.github/workflows/release.yaml`, which:
   - builds (or restores from cache) the DuckDB static bundle for linux/amd64 — roughly 30 minutes on a cold cache, seconds on a warm one;
   - cross-compiles the four platform archives (linux and darwin, amd64 and arm64) inside `goreleaser-cross`;
   - pushes `ghcr.io/bino-bi/sluice:<version>` plus the per-arch tags and the multi-arch manifest;
   - signs `checksums.txt` and the image manifests with keyless cosign, attaches CycloneDX SBOMs, and appends its footer to the notes you wrote;
   - generates SLSA v1.0 provenance (`multiple.intoto.jsonl`) in a follow-up job.
6. **Verify** the assets landed and the image pulls:

   ```bash
   docker pull ghcr.io/bino-bi/sluice:X.Y.Z
   cosign verify ghcr.io/bino-bi/sluice:X.Y.Z \
     --certificate-identity-regexp 'https://github.com/bino-bi/sluice/.*' \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com
   ```

Pre-release tags (`-rc.N`, `-beta.N`) are handled automatically: goreleaser's `prerelease: auto` flags the GitHub release, `make_latest` is suppressed, and the `latest` Docker tags and manifest are skipped so a candidate never becomes the default pull.

If the workflow fails partway, fix the cause and re-run the job — `replace_existing_artifacts` makes asset upload idempotent. Deleting and recreating the tag is not necessary.

## Pull requests

- Small, focused diffs. One logical change per PR.
- Tests alongside code (`*_test.go`). Integration tests (deferred to later slice) will live under `tests/integration/`.
- CI must be green before review.

## Security

Do not file security issues as public GitHub issues. See `SECURITY.md` (landing in a later slice) for the coordinated disclosure process.
