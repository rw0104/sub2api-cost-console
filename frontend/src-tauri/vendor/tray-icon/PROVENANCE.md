# tray-icon dependency patch

This directory contains the published `tray-icon` 0.24.2 library from
[tauri-apps/tray-icon](https://github.com/tauri-apps/tray-icon).
It is a host desktop dependency, separate from Sub2API plugins.

- Published crate: https://crates.io/crates/tray-icon/0.24.2
- Upstream source revision: `e05dab06db5b441efe5c26f9be2e0c29f1ba2089`
- Original `.crate` SHA-256:
  `045979e3f037cd18ad1cb2a419dfda133c5c29c9f3453370079f2255d46c257e`
- License: MIT OR Apache-2.0. Original license files and copyright notices are retained.

The normalized published Cargo.toml, README, licenses and all platform library
source files are retained. Registry cache markers, the upstream development lock
file and the unnormalized manifest are omitted because they are not used when
this library is a dependency of the host.

## Local patch

Only the Windows rectangle result handling differs from upstream:

- `src/platform_impl/windows/mod.rs` passes the HRESULT and returned rectangle
  to the new `rect_result.rs` helper instead of requiring `S_OK` exactly.
- `src/platform_impl/windows/rect_result.rs` accepts non-negative HRESULTs only
  with a positive-area rectangle, including `S_FALSE` with valid coordinates.
  Negative HRESULTs and empty or inverted rectangles are rejected.

The installed Windows shell was observed returning `S_FALSE` together with valid
notification-area coordinates. Upstream's `S_OK`-only condition discards those
coordinates and the tray window procedure returns before dispatching the click.
The [HRESULT success convention](https://learn.microsoft.com/en-us/windows/win32/api/winerror/nf-winerror-succeeded)
defines non-negative status values as success. The
[Shell_NotifyIconGetRect documentation](https://learn.microsoft.com/en-us/windows/win32/api/shellapi/nf-shellapi-shell_notifyicongetrect)
documents `S_OK`; the `S_FALSE` behavior described here is based on the observed
Windows environment, not an additional guarantee stated in that API reference.

The helper's tests cover `S_OK`, `S_FALSE`, failure HRESULTs, negative screen
coordinates and unusable rectangles. They can run directly against the production
module using `rustc --test src/platform_impl/windows/rect_result.rs --edition 2021`
with an output path outside this source directory. The host's native tray regression
also checks that real tray window messages reach the registered event handler.

Remove this patch when a reviewed upstream release includes equivalent behavior;
update the host lockfile and rerun the native tray regression before removal.
