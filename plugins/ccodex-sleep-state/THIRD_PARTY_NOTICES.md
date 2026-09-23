# Third-party notices

This plugin uses the Sub2API public plugin SDK from the host repository and
the Go gRPC runtime. The plugin does not bundle the host source tree; the
runtime package contains the license files required by the host package
format.

The original `ccodex-sleep-state` project is an independent GPL-3.0 project.
Version 0.5.0 embeds its gateway, turnstate, settings, routepool and fsutil core
from commit `b18fabf9ad8e9d7af7d9d0306b623ba6091a39d6` under `internal/upstream`,
including the original regression suites. Its license is bundled at
`licenses/CCodex-Sleep-State-LICENSE`. Import paths and host adapter changes
are documented in `provenance/source-manifest.json` and `docs/CORE.md`.
The standalone local service and Codex configuration editor are not invoked.

The runtime embeds Mihomo v1.19.31 to parse and dial supported subscription
protocols. Mihomo is distributed under GPL-3.0; its license is included at
`licenses/Mihomo-LICENSE`. Transitive dependency notices remain governed by
their respective upstream licenses and the locked Go module graph.
