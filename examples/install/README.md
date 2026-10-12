<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# install

Commands for the Install and Quickstart pages. The check cannot run most of them: they need Git, Docker, a network or cosign.

| File | Content |
|---|---|
| `steps/01-help.sh` | Lists the commands of the binary. The check runs it. |
| `manual/build.sh` | Clone the repository and build `bin/sluice`. |
| `manual/start-example.sh` | Start the `hello-sluice` example. |
| `manual/second-terminal.sh` | Prepare a second terminal for the steps of `hello-sluice`. |
| `manual/docker-example.sh` | Run `hello-sluice` in an image built from the checkout. |
| `manual/image.sh` | Pull a released image and verify its signature. |
| `manual/archive.sh` | Download a release archive, check it and verify its signature. |

Files in `manual/` get a syntax check only. Run them from the repository root. See [the examples guide](../README.md) for the layout.
