<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Install

Run Sluice from a container image, a release archive or a source build.

These docs describe the `main` branch. A release can behave differently. The API and the policy schema can still change, so pin an exact version.

## Container image

The image runs on `linux/amd64` and `linux/arm64`. It has no shell and runs as a non-root user. The tag is the release version without the leading `v`. A pre-release never gets the tag `latest`. Set `VERSION` to the release that you want.

```bash
--8<-- "examples/install/manual/image.sh:pull"
```

The image is signed. Verify it with [cosign](https://docs.sigstore.dev/):

```bash
--8<-- "examples/install/manual/image.sh:cosign"
```

## Release archive

The [releases page](https://github.com/bino-bi/sluice/releases) has archives for `linux_amd64`, `linux_arm64`, `darwin_amd64` and `darwin_arm64`. The Linux binaries need glibc 2.38 or later, for example Debian 13 or Ubuntu 24.04. On an older system or on Windows, use the image or build from source. This example gets and checks the `linux_amd64` archive:

```bash
--8<-- "examples/install/manual/archive.sh:download"
```

A release also holds a signature for `checksums.txt` and, for each archive, a CycloneDX SBOM that lists its software parts. `multiple.intoto.jsonl` holds the SLSA provenance, a signed build record. Verify the signature:

```bash
--8<-- "examples/install/manual/archive.sh:cosign"
```

## Build from source

You need Git, `make`, Go 1.26 or later and a C compiler. Sluice needs cgo, so a build with `CGO_ENABLED=0` fails.

```bash
--8<-- "examples/install/manual/build.sh"
```

`make build` writes `bin/sluice`. `sluice version` prints the version, the commit, the build time and the parser. `make all` also formats, checks and tests the code.

## Check the binary

The binary lists its commands:

```bash
--8<-- "examples/install/steps/01-help.sh"
```

```text
--8<-- "examples/install/expected/01-help.txt"
```

## DuckDB extensions

The images hold every DuckDB extension that Sluice needs. Other builds can download an extension when they first attach a data source. [Deployment](../operations/deployment.md) lists what each build includes and how to build your own image.

## See also

- [Quickstart](index.md): run the example.
- [Server config](../operations/server-config.md): the server config and the policy directory.
- [CLI](../reference/cli.md): all commands and flags.
