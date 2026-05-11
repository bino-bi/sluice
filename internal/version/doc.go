// SPDX-License-Identifier: AGPL-3.0-or-later

// Package version exposes the build-time identity of the running binary:
// semver, git commit, build timestamp, Go toolchain, and platform. Values
// are injected via -ldflags and fall back to debug.ReadBuildInfo().
package version
