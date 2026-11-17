// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
)

// startReloads starts the reload triggers that serve and mcp share: the
// fsnotify watcher when policies.reload is on, and SIGHUP. Both stop with
// ctx. Without the SIGHUP handler the signal would end the process.
func startReloads(ctx context.Context, deps *runtimeDeps) {
	if deps.watcher == nil {
		return
	}
	// fsnotify watching is opt-in; SIGHUP and /admin/reload stay available
	// regardless via the watcher's synchronous Reload path.
	if deps.server.Policies.Reload {
		deps.watcher.Start(ctx)
	}
	// SIGHUP is Unix-only; /admin/reload is the cross-platform fallback.
	hupCh := make(chan os.Signal, 1)
	signal.Notify(hupCh, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-ctx.Done():
				signal.Stop(hupCh)
				return
			case <-hupCh:
				if _, err := deps.watcher.Reload(ctx); err != nil {
					deps.log.Warn("SIGHUP reload failed", "error", err.Error())
				}
			}
		}
	}()
}

// errPinnedIdentity is the cause when a reload invalidates the pinned
// identity of the stdio transport.
var errPinnedIdentity = errors.New("the pinned identity is no longer valid after a reload; restart with a valid credential")

// pinnedCred is the static credential of the stdio transport and the
// identity it resolved to at start.
type pinnedCred struct {
	user        *identity.UserCtx
	jwt, apiKey string
}

// recheckTimeout bounds one check. It runs under the reload lock, and a
// JWT check can fetch JWKS.
const recheckTimeout = 10 * time.Second

// recheckPinned checks the pinned stdio identity after each published
// reload, with the identity state that reload applied. A credential that
// no longer resolves (revoked, or a JWT past its exp), or now resolves to
// another identity (subject, issuer, groups, claims), calls stop: the
// process must not keep the rights of the old snapshot.
func recheckPinned(deps *runtimeDeps, pc *pinnedCred, stop context.CancelCauseFunc) {
	deps.registry.Subscribe(func(_, cur *config.Snapshot) {
		if cur == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), recheckTimeout)
		defer cancel()
		u, err := resolveStaticIdentity(ctx, deps.identifier, pc.jwt, pc.apiKey)
		if err == nil && !sameIdentity(u, pc.user) {
			err = errors.New("the credential now resolves to another identity")
		}
		if err != nil {
			deps.log.Error("mcp: stopping, the pinned identity is no longer valid", "error", err.Error())
			stop(fmt.Errorf("%w: %w", errPinnedIdentity, err))
		}
	})
}

func sameIdentity(a, b *identity.UserCtx) bool {
	return a.Subject == b.Subject && a.Issuer == b.Issuer && a.Email == b.Email &&
		reflect.DeepEqual(a.Groups, b.Groups) && reflect.DeepEqual(a.Claims, b.Claims)
}
