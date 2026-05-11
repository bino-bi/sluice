<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Configuration reload

Sluice reloads policies, bindings, and data sources without a restart
— queries in flight finish against the pre-reload snapshot; new
queries see the post-reload snapshot atomically.

## Triggers

| Trigger             | When to use                                             |
| ------------------- | ------------------------------------------------------- |
| fsnotify watcher    | Default. GitOps commits land in the policies directory. |
| `SIGHUP`            | CI tooling, one-off `kill -HUP` after scp.              |
| `POST /admin/reload`| Operator UI / automation.                               |

All three funnel into `config.Registry.Publish(snapshot)`. Subscribers
(`policy.Engine.ApplySnapshot` and `schema.Cache.InvalidateAll`) fan
out.

## Debounce

The fsnotify path coalesces a burst of editor writes within 250 ms into
a single reload. Manual `Reload(ctx)` (SIGHUP + admin) skips the
debounce.

## What doesn't reload without a restart

- Listener addresses (`rest.listen`, `mcp.listen`, `admin.listen`).
- Executor pool sizing.
- The audit genesis seed (changing it would break the chain).

These are deliberately static — changing them triggers `serve` to refuse
the reload with a clear error.

## Observing reloads

Every publish increments `sluice_config_reloads_total` and logs at
`INFO` with the new snapshot version.
