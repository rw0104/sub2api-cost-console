"""Exercise signed UI assets through the real Sub2API handler test fixture."""

import argparse
import json
import os
from pathlib import Path
from urllib.request import urlopen

from playwright.sync_api import expect, sync_playwright


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", required=True)
    args = parser.parse_args()
    origin = args.url.rstrip("/")
    report_dir = os.environ.get("SUB2API_CCODEX_UI_REPORT_DIR")
    if report_dir:
        report_dir = Path(report_dir).resolve()
        if "dist" in (part.lower() for part in report_dir.parts):
            raise ValueError("browser evidence belongs in .build, never the release dist directory")
        report_dir.mkdir(parents=True, exist_ok=True)
    with urlopen(origin + "/fixture/sources", timeout=5) as response:
        sources = json.load(response)
    errors = []
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1100, "height": 900})
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.goto(origin + "/fixture/parent")
        page.wait_for_function("window.ready === true")
        frame = page.frame_locator("#plugin")
        frame.locator("#fetch-nodes").wait_for()
        page.wait_for_function("window.bridgeCalls.includes('config.load')")
        frame.locator("#status").filter(has_text="配置已加载").wait_for()
        if "X-Codex-Turn-State" not in frame.locator("#policy").inner_text():
            raise AssertionError("core request header is not a first-class UI capability")
        for control in ("state-verification", "verify-all-states", "verification-session", "allow-passthrough"):
            expect(frame.locator(f"#{control}")).to_be_visible()
        frame.locator("#closed").check()
        frame.locator("#account-mode").select_option("team")
        frame.locator("#state-refresh-mode").select_option("standby")
        frame.locator("#max-probes").fill("8")
        frame.locator("#pool-enabled").check()
        frame.locator("#body-limit").fill("134217728")
        frame.locator("#zstd-window").fill("80")
        frame.locator("#compact-limit").fill("96")
        frame.locator("#enable-protection").click()
        frame.locator("#status").filter(has_text="请求头保护已开启").wait_for(timeout=40000)
        persisted_core = page.evaluate("""async () => {
            const c=await fetch('/api/v1/admin/plugins/1/config').then(r=>r.json());
            return Object.fromEntries(['enabled','inject_state','harvest_on_demand','fail_closed','account_mode','state_refresh_mode','max_probes_per_round','pool_enabled','max_body_bytes','zstd_window_mib','compact_limit_mib'].map(k=>[k,c[k]]));
        }""")
        if persisted_core != {"enabled": True, "inject_state": True, "harvest_on_demand": True, "fail_closed": True, "account_mode": "team", "state_refresh_mode": "standby", "max_probes_per_round": 8, "pool_enabled": True, "max_body_bytes": 134217728, "zstd_window_mib": 80, "compact_limit_mib": 96}:
            raise AssertionError("signed UI core controls did not persist through the real host")
        frame.locator("#subscriptions").fill(sources["subscription_url"])
        frame.locator("#fetch-nodes").click()
        frame.locator("#nodes-body [data-action='select']").nth(1).wait_for(timeout=40000)
        select = frame.locator("#nodes-body [data-action='select']").first
        selected_id = select.get_attribute("data-node-id")
        if not selected_id:
            raise AssertionError("discovered node must expose its stable selection ID")
        frame.locator("#test-nodes").click()
        page.wait_for_function("""async () => {
            const config=await fetch('/api/v1/admin/plugins/1/config').then(r=>r.json());
            return config.route_report?.nodes?.length===2 && config.route_report.nodes.every(n=>n.status==='unavailable' && n.error_code);
        }""", timeout=40000)
        frame.locator("#nodes-body [data-action='select']").first.click()
        page.wait_for_function("""async id => {
            const config=await fetch('/api/v1/admin/plugins/1/config').then(r=>r.json());
            return config.route_mode==='fixed' && config.fixed_route_id===id;
        }""", arg=selected_id, timeout=40000)
        persisted_nodes = page.evaluate("""async () => {
            const config=await fetch('/api/v1/admin/plugins/1/config').then(r=>r.json());
            return config.route_report?.nodes?.length || 0;
        }""")
        if persisted_nodes != 2:
            if report_dir:
                page.screenshot(path=str(report_dir / "host-selection-failure.png"), full_page=True)
            raise AssertionError("selecting a node discarded the stored route report")
        # Reload proves diagnostics and node selection survive the temporary
        # process used while the host plugin itself remains disabled.
        page.reload()
        page.wait_for_function("window.ready === true")
        frame = page.frame_locator("#plugin")
        frame.locator("#nodes-body [data-action='select']").nth(1).wait_for(timeout=40000)
        # Reading core status through config.save must work while the host
        # plugin is disabled, without inventing a ready OAuth state.
        frame.locator("#refresh-core").click()
        expect(frame.locator("#refresh-core")).to_be_enabled(timeout=40000)
        core_report = page.evaluate("""async () => {
            const c=await fetch('/api/v1/admin/plugins/1/config').then(r=>r.json());
            return c.core_report;
        }""")
        if core_report.get("schema") != 1 or core_report.get("sessions") or core_report.get("engines") != 0 or core_report.get("requests_total") != 0:
            raise AssertionError("disabled plugin fabricated a live or ready state session")
        if frame.locator("#sessions-body button").count() or not frame.locator("#retry-route").is_disabled():
            raise AssertionError("signed UI offers paid state collection without a live session")
        for control in ("verify-all-states", "verify-one-state", "verification-session", "cancel-verification"):
            expect(frame.locator(f"#{control}")).to_be_disabled()
        if "暂无可用会话" not in frame.locator("#verification-status").inner_text():
            raise AssertionError("node state qualification did not explain its missing runtime session")
        if "尚未验证" not in frame.locator("#verification-body").inner_text():
            raise AssertionError("connectivity diagnostics were mistaken for actual state qualification")
        if "快照时间" not in frame.locator("#core-snapshot").inner_text():
            raise AssertionError("signed UI does not distinguish runtime snapshot from live status")

        # An explicitly selected formal egress stays separate from the node
        # used to collect state. Updating it must retain diagnostic results.
        second_id = frame.locator("#nodes-body [data-action='select']").nth(1).get_attribute("data-node-id")
        frame.locator("#egress-mode").select_option("fixed")
        frame.locator("#egress-route").select_option(second_id)
        frame.locator("#save").click()
        frame.locator("#status").filter(has_text="配置已保存").wait_for(timeout=40000)
        egress = page.evaluate("""async () => {
            const c=await fetch('/api/v1/admin/plugins/1/config').then(r=>r.json());
            return {mode:c.egress_mode,route:c.egress_route,collector:c.fixed_route_id,snapshot:c.core_report};
        }""")
        if egress.get("mode") != "fixed" or egress.get("route") != second_id or egress.get("collector") != selected_id:
            raise AssertionError("formal egress and state collector were not independently persisted")
        if egress.get("snapshot") or frame.locator("#sessions-body button").count():
            raise AssertionError("saving core policy retained a stale ready snapshot")
        frame.locator("#refresh-core").click()
        expect(frame.locator("#refresh-core")).to_be_enabled(timeout=40000)

        # Each command uses a new temporary process. The pool's disk-backed
        # lifecycle must survive disable, reload, refresh and later recycle.
        pool_row = frame.locator(f'#pool-body tr[data-pool-id="{selected_id}"]')
        pool_row.locator('[data-pool-action="disabled"]').click()
        expect(frame.locator("#refresh-core")).to_be_enabled(timeout=40000)
        expect(pool_row).to_contain_text("已停用")
        page.reload()
        page.wait_for_function("window.ready === true")
        frame = page.frame_locator("#plugin")
        frame.locator("#status").filter(has_text="配置已加载").wait_for()
        frame.locator("#refresh-core").click()
        expect(frame.locator("#refresh-core")).to_be_enabled(timeout=40000)
        pool_row = frame.locator(f'#pool-body tr[data-pool-id="{selected_id}"]')
        expect(pool_row).to_contain_text("已停用")
        expect(pool_row.locator('[data-pool-action="disabled"]')).to_be_disabled()
        pool_row.locator('[data-pool-action="available"]').click()
        expect(frame.locator("#refresh-core")).to_be_enabled(timeout=40000)
        expect(pool_row).to_contain_text("可用")
        page.reload()
        page.wait_for_function("window.ready === true")
        frame = page.frame_locator("#plugin")
        frame.locator("#status").filter(has_text="配置已加载").wait_for()
        frame.locator("#max-probes").fill("11")
        frame.locator("#refresh-core").click()
        expect(frame.locator("#refresh-core")).to_be_enabled(timeout=40000)
        expect(frame.locator(f'#pool-body tr[data-pool-id="{selected_id}"]')).to_contain_text("可用")
        if frame.locator("#max-probes").input_value() != "11":
            raise AssertionError("real core status refresh erased unsaved form input")
        final_snapshot = page.evaluate("""async () => {
            const c=await fetch('/api/v1/admin/plugins/1/config').then(r=>r.json());
            return {probes:c.max_probes_per_round,nodes:c.route_report.nodes,command:c.core_request,sessions:c.core_report.sessions};
        }""")
        if final_snapshot.get("probes") != 8 or final_snapshot.get("command") or final_snapshot.get("sessions"):
            raise AssertionError("core status persisted draft settings, a command or a fabricated live session")
        if len(final_snapshot.get("nodes", [])) != 2 or any(node.get("status") != "unavailable" for node in final_snapshot["nodes"]):
            raise AssertionError("core lifecycle actions discarded node connectivity diagnostics")
        if report_dir:
            frame.locator("#policy").evaluate("el => el.scrollIntoView({block:'start'})")
            page.screenshot(path=str(report_dir / "host-core-controls.png"), full_page=True)
            frame.locator("#state-sessions").scroll_into_view_if_needed()
            page.screenshot(path=str(report_dir / "host-core-empty-state.png"), full_page=True)
            frame.locator("#state-verification").scroll_into_view_if_needed()
            page.screenshot(path=str(report_dir / "host-state-qualification.png"), full_page=True)
            frame.locator("#pool-panel").scroll_into_view_if_needed()
            page.screenshot(path=str(report_dir / "host-pool-recycled.png"), full_page=True)
        if report_dir:
            frame.locator("#nodes").scroll_into_view_if_needed()
            page.screenshot(path=str(report_dir / "host-node-results.png"), full_page=True)
        frame.locator("#subscriptions").fill(sources["invalid_subscription_url"])
        frame.locator("#fetch-nodes").click()
        page.wait_for_function("""async () => {
            const config=await fetch('/api/v1/admin/plugins/1/config').then(r=>r.json());
            return !!config.route_report?.error_code;
        }""", timeout=40000)
        if report_dir:
            frame.locator("#nodes").scroll_into_view_if_needed()
            page.screenshot(path=str(report_dir / "host-source-error.png"), full_page=True)
        # Status is unsupported for this v2 runtime on stock 0.2.7. The new
        # diagnostic workflow must use normalized config, not that endpoint.
        if any(method not in ("config.load", "config.save") for method in page.evaluate("window.bridgeCalls")):
            raise AssertionError("signed UI still depends on unsupported runtime bridge methods")
        if errors:
            raise AssertionError(f"browser errors: {errors}")
        browser.close()
    print(json.dumps({"passed": True, "real_host": True, "flows": ["signed_assets", "core_controls", "core_empty_snapshot", "qualification_controls", "qualification_requires_live_session", "independent_egress", "pool_disable_restart_recycle", "unsaved_input_preserved", "discover", "per_node_errors", "select", "reload", "invalid_subscription"]}))


if __name__ == "__main__":
    main()
