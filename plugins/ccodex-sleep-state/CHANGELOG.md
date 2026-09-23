# Changelog

## 0.5.1 - restore proxy forwarding and verify state per node

- Fix parseable but nonqualifying state headers incorrectly marking healthy proxy transports failed; migrate only those old misclassifications.
- Map pre-dispatch local policy refusals to the existing host terminal protection error instead of upstream 503/account failover.
- Bound complete foreground collection rounds, support explicitly configured models, and rotate ordinary fallback routes.
- Replace permanent unclassified HTTP 403 poisoning with a credential-scoped finite pause that honors Retry-After; preserve 401 and 429 guards.
- Add cancellable per-node Responses header verification with independent presence, parsing, actual length/blocks and policy qualification results. Jobs use RAM-only live sessions, work without injection, respect budgets/cooldowns, and never replace active state.
- Add signed-process CONNECT/TLS/SSE success and failure regressions that reproduce the 0.5.0 outage.

## 0.5.0 - embedded upstream turn-state core

- Embed gateway, turnstate, settings, routepool and fsutil from upstream b18fabf9ad8e9d7af7d9d0306b623ba6091a39d6, retaining original tests and license.
- Preserve the six upstream probe identity headers while excluding original conversation/state/encoding headers; inject and relay X-Codex-Turn-State through the actual host transport.
- Add credential/workspace/model isolation, bounded serialized harvesting, shared account rejection guards, held active/standby modes, Team rules and response shape rejection without replay.
- Add bounded gzip/zstd decoding, V1/V2 compaction and independent formal egress policies.
- Persist controlled node lifecycle with cross-process locks; add live state diagnostics, manual collection and node recycle/disable controls.
- Preserve existing publisher identity and 0.4.0 source/node configuration; keep explicit legacy request limits and migrate old state refresh behavior.

## 0.4.0 - node discovery, testing and selection

- Added a node table with discovery, per-node and all-node tests, latency/status and persistent selection.
- Removed the UI dependency on the host's unsupported v2 status endpoint, which made successful tests appear to fail.
- Bounded each diagnostic action to 12 seconds and return safe source errors and incomplete-node results in normalized configuration.
- Preserved subscription filters/User-Agent and account proxy inheritance across saves; fixed standard HTTP default-port parsing.
- Added signed-package production-handler browser tests and actual localhost CONNECT/SOCKS forwarding regressions.

## 0.3.1 - resilient subscription refresh

- Added lazy subscription refresh every 15 minutes by default.
- Invalidates and reloads the route pool after all probes fail, a saved route disappears, or a formal connection fails.
- Keeps the last working registry when a scheduled subscription refresh temporarily fails, then retries after 60 seconds.

## 0.3.0 - authenticated proxies and Mihomo subscriptions

- Added authenticated HTTP/HTTPS proxy URL support.
- Added Mihomo v1.19.31 adapters for AnyTLS, SS, SSR, VMess, VLESS, Trojan, Hysteria, Hysteria2 and TUIC sources.
- Added subscription download, Base64/URI/YAML parsing and automatic per-route OpenAI connectivity tests.
- Added safe aggregate availability reporting to the configuration UI.

## 0.2.1 - verified multi-route package

- Verified compatibility with Sub2API 0.2.7.
- Fixed UI source controls, fixed-route configuration, and dynamic iframe height reporting.

## 0.2.0 - multi-route standard sources

- Added explicit multi-route configuration with `proxy_urls`, `direct`, legacy `proxy_url` migration and account-level route overrides.
- Added route registry caching, round-robin probe selection, state-to-route binding, route reload cleanup and missing-route fail-closed behavior.
- Updated the Bridge UI and browser harness for multiple proxy URLs with duplicate removal.
- Added bounded YAML, URI and Base64 subscription parsing for standard HTTP/HTTPS/SOCKS5/SOCKS5H nodes; Mihomo-specific outbound protocol adapters remain outside this slice.

## 0.1.0 - development candidate

- Added Sub2API v2 `openai.oauth.protection_transport.v1` runtime.
- Added bounded on-demand turn-state probing and account/model-isolated memory cache.
- Added HTTP, HTTPS, SOCKS5 and SOCKS5H outbound transport support.
- Added 401/403 account blocking, 429 cooldown, response timeout and no-replay semantics.
- Added UI Bridge configuration, signed multi-runtime packaging, package verification and process smoke tests.
- Added Windows amd64, Linux amd64 and Linux arm64 runtime builds.

This candidate has not been validated against production credentials or production traffic. Linux artifacts are cross-compiled and format-verified but have not completed real process execution.
