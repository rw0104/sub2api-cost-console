# Embedded upstream core

Source: https://github.com/gylive/ccodex-sleep-state

Pinned commit: `b18fabf9ad8e9d7af7d9d0306b623ba6091a39d6` (retrieved 2026-09-19).

The `gateway`, `turnstate`, `settings`, `routepool`, and `fsutil` directories
include upstream source and its complete Go regression tests. Imports were
mechanically changed to `local.sub2api/ccodex-sleep-state/internal/upstream/`.
The upstream GPL-3.0 license is reproduced in `LICENSE`.

Plugin-specific changes:

- `proxyroute/route.go` bridges immutable plugin-owned HTTP transports and stable
  route identifiers. The host-facing plugin owns subscription loading and adapter
  lifetime; the standalone Codex installer/server is not run inside the host.
- `gateway/adapter.go` adds a formal-dispatch observer and process-local shared
  credential-limit/probe-slot bookkeeping. Engine rebuilds cannot reset a known
  401/403/429 response; only digests and rejection metadata enter this bookkeeping.
  Shared session reference counts retain guards across draining generations and
  collect obsolete healthy credentials after retirement; blocked guards persist.
- `gateway/engine.go` uses shared bookkeeping when configured, preserves bounded
  standalone behavior otherwise, and honors an explicit disabled-harvest policy.
- `settings/settings.go` adds adapter-only options for disabling harvest,
  enforcing exact byte limits, and applying the host configuration model allowlist.
- `gateway/handler.go` applies that allowlist, notifies formal dispatch, and
  prevents an upstream response from forging the local-error marker. The framed
  plugin adapter converts post-dispatch local failures into `RequestSent=true`
  failures so the host cannot replay a potentially billed generation.

The plugin adapter resides in `internal/transport/core.go`; its local HTTPS
integration tests exercise the actual embedded engine and framed response stream.
Policy-only injection/refresh-mode changes use upstream's atomic setters and keep
state and cooldowns. Route replacement immediately cancels the old background
worker while allowing already-dispatched requests to finish on their snapshot.
Additional pool persistence integration changes, if present, are documented next
to the routepool implementation. No OAuth refresh or Codex credential-file access
is included in this plugin core.
