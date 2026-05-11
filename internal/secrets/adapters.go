// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"

	pkgdatasource "github.com/bino-bi/sluice/pkg/datasource"
	pkgmask "github.com/bino-bi/sluice/pkg/mask"
)

// NewSaltStore adapts a Resolver to pkg/mask.SaltStore so the hash mask
// provider (v1) can fetch per-column salts without knowing about secret URIs.
func NewSaltStore(r *Resolver) pkgmask.SaltStore {
	return resolverAdapter{r: r}
}

// NewKeyStore adapts a Resolver to pkg/mask.KeyStore for the fpe mask
// provider (v1).
func NewKeyStore(r *Resolver) pkgmask.KeyStore {
	return resolverAdapter{r: r}
}

// NewDataSourceResolver adapts a Resolver to pkg/datasource.SecretResolver so
// driver factories (v1) can resolve credentials from their attach specs.
func NewDataSourceResolver(r *Resolver) pkgdatasource.SecretResolver {
	return resolverAdapter{r: r}
}

// resolverAdapter satisfies all three interfaces (SaltStore, KeyStore,
// SecretResolver). They share the same signature — one method, context +
// reference → bytes — so a single adapter covers them all.
type resolverAdapter struct{ r *Resolver }

// Get implements pkg/mask.SaltStore and pkg/mask.KeyStore.
func (a resolverAdapter) Get(ctx context.Context, ref string) ([]byte, error) {
	return a.r.Resolve(ctx, ref)
}

// Resolve implements pkg/datasource.SecretResolver.
func (a resolverAdapter) Resolve(ctx context.Context, ref string) ([]byte, error) {
	return a.r.Resolve(ctx, ref)
}
