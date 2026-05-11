// SPDX-License-Identifier: Apache-2.0

// Package mask defines the column-masking Provider interface, a registry for
// plugging in mask types, and the built-in providers (null, constant,
// partial, hash). The Args type mirrors apitypes.MaskArgs; drift between the
// two is caught by the mirror test.
package mask
