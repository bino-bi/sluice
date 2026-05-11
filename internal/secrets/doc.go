// SPDX-License-Identifier: AGPL-3.0-or-later

// Package secrets resolves secret:// URIs to byte blobs with caching and
// invalidation. The MVP slice ships env:// and file:// providers only;
// vault, aws-sm, and gcp-sm are deferred behind build tags in a later slice.
package secrets
