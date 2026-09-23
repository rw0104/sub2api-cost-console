import json
import mimetypes
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit

from playwright.sync_api import sync_playwright


ROOT = Path(__file__).resolve().parent.parent
TOKEN = "ui-test-token"
SOURCE_URL = "https://example.test/nodes?token=fixture-only&filter=a,b"

# This harness tests browser/bridge behavior only. test-host-ui.py separately
# verifies these operations against the real signed runtime and host handlers.
HARNESS = """<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Plugin UI bridge contract fixture</title>
<style>html,body{margin:0;background:#eef1f5}iframe{display:block;width:100%;height:1800px;border:0}</style>
<iframe id="plugin" sandbox="allow-scripts" src="/ui/index.html#bridge_token=__TOKEN__"></iframe>
<script>
window.__ready = false;
window.__saved = {revision:3,fail_closed:true,max_body_bytes:4194304, subscriptions:[{url:'__SOURCE__',user_agent:'fixture-agent',include_protocols:['http','socks5'],exclude_keywords:['expired']}],accounts:{'7':{enabled:true}}};
window.__resize = 0;
window.__calls = [];
window.__nextError = '';
window.__nextLoadError = false;
window.__replyDelay = 0;
window.__noSessions = false;
window.__poolState = 'exhausted';
window.__verification = null;
const nodes = [
  {id:'route-aaaa',name:'本地 HTTP',protocol:'http',status:'untested'},
  {id:'route-bbbb',name:'本地 SOCKS5',protocol:'socks5',status:'untested'}
];
window.addEventListener('message', event => {
  const data = event.data;
  if (event.source !== document.querySelector('iframe').contentWindow || !data || data.source !== 'sub2api-plugin-ui' || data.bridge_token !== '__TOKEN__') return;
  if (data.type === 'sub2api.plugin.ready') { window.__ready = true; return; }
  if (data.type === 'ui.resize') { window.__resize = data.height; document.querySelector('iframe').style.height = data.height + 'px'; return; }
  if (!data.request_id) return;
  window.__calls.push(structuredClone(data));
  const reply = {source:'sub2api-plugin-host',bridge_token:'__TOKEN__',request_id:data.request_id,ok:true};
  if (data.type === 'config.load') {
    if (window.__nextLoadError) { window.__nextLoadError = false; reply.ok = false; reply.error = 'internal error'; }
    else reply.config = window.__saved;
  } else if (data.type === 'config.save') {
    const next = structuredClone(data.config);
    const action = next.route_request;
    const core = next.core_request;
    delete next.route_request;
    delete next.core_request;
    next.revision = window.__saved.revision + 1;
    if (action) {
      const rows = nodes.map(node => ({...node, ...(next.route_report?.nodes?.find(old => old.id === node.id) || {})}));
      if (action.operation === 'test') rows.forEach((row, i) => {
        if (!action.route_id || row.id === action.route_id) {
          row.status = i ? 'restricted' : 'unavailable';
          row.error_code = i ? 'http_restricted' : 'proxy_auth';
          row.latency_ms = i ? 34 : 12;
          if (i) row.http_status = 403;
        }
      });
      next.route_report = {schema:1,source_signature:'fixture-only',generated_at:new Date().toISOString(),nodes:window.__nextError ? [] : rows};
      if (window.__nextError) { next.route_report.error_code = window.__nextError; window.__nextError = ''; }
    }
    if (core) {
      if (core.operation === 'verify_nodes') {
        const selection = core.route_ids?.length ? nodes.filter(n => core.route_ids.includes(n.id)) : nodes;
        window.__verification = {session_id:core.session_id,account_id:7,model:'gpt-6-astra',state:core.route_ids?.length?'completed':'cooling_down',total:selection.length,completed:1,next_run_at:new Date(Date.now()+180000).toISOString(),rows:selection.map((n,i)=>({id:n.id,name:n.name,protocol:n.protocol,status:n.id==='route-aaaa'?'shape_mismatch':core.route_ids?.length?'accepted':'pending',header_present:n.id==='route-aaaa'||!!core.route_ids?.length,parsed:true,qualified:n.id==='route-bbbb'&&!!core.route_ids?.length,expected_length:292,observed_length:n.id==='route-aaaa'?312:292,expected_blocks:10,observed_blocks:n.id==='route-aaaa'?11:10,http_status:200,latency_ms:450,checked_at:new Date().toISOString()}))};
      }
      if (core.operation === 'cancel_verification' && window.__verification) window.__verification.state='cancelled';
      if (core.operation === 'pool') window.__poolState = core.action;
      const sessions = [
        {id:'session-a',model:'gpt-6-astra',account_id:7,expected_length:292,observed_length:292,usable:true,ready:true,remaining_seconds:3200,cooldown_seconds:0,phase:'ready',diagnostic_message:'主用 state 有效'},
        {id:'session-b',model:'gpt-5.6-sol',account_id:8,expected_length:300,observed_length:0,usable:false,ready:false,remaining_seconds:0,cooldown_seconds:120,rejected_status:429,phase:'rate_limited',diagnostic_message:'等待限流冷却'},
        {id:'session-c',model:'gpt-5.6-terra',account_id:9,expected_length:292,observed_length:0,usable:false,ready:false,remaining_seconds:0,cooldown_seconds:0,phase:'collecting',diagnostic_message:'正在采集'}
      ];
      next.core_report = {schema:1,generated_at:new Date().toISOString(),engines:window.__noSessions ? 0 : 1,requests_total:8,sessions:window.__noSessions ? [] : sessions,pool:[{id:'route-aaaa',name:'本地 HTTP',protocol:'http',state:window.__poolState,attempts:6,reason:'fixture-only'}]};
      next.core_report.node_verifications = window.__verification ? [window.__verification] : [];
    }
    window.__saved = next;
    reply.config = next;
  } else {
    reply.ok = false; reply.error = 'unsupported bridge method: ' + data.type;
  }
  setTimeout(() => event.source.postMessage(reply, '*'), window.__replyDelay);
});
</script>
""".replace("__TOKEN__", TOKEN).replace("__SOURCE__", SOURCE_URL).encode()


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        path = urlsplit(self.path).path
        if path == "/harness.html":
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.end_headers()
            self.wfile.write(HARNESS)
            return
        if path.startswith("/ui/"):
            target = (ROOT / path.lstrip("/")).resolve()
            ui_root = (ROOT / "ui").resolve()
            if target.parent != ui_root or not target.is_file():
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header("Content-Type", mimetypes.guess_type(target.name)[0] or "application/octet-stream")
            self.end_headers()
            self.wfile.write(target.read_bytes())
            return
        self.send_error(404)

    def log_message(self, *_):
        return


def main():
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    errors = []
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True)
            page = browser.new_page(viewport={"width": 900, "height": 900})
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.goto(f"http://127.0.0.1:{server.server_port}/harness.html")
            page.wait_for_function("window.__ready === true")
            frame = page.frame(url=lambda url: "/ui/index.html" in url)
            if frame is None:
                raise RuntimeError("plugin iframe did not load")
            frame.locator("#status").filter(has_text="配置已加载").wait_for()
            if frame.locator("#subscriptions").input_value() != SOURCE_URL:
                raise RuntimeError("initial subscription configuration did not load")
            if frame.locator("#body-limit").input_value() != "4194304":
                raise RuntimeError("upgrade changed a previously configured body limit")
            frame.locator("#enable-protection").click()
            frame.locator("#status").filter(has_text="请求头保护已开启").wait_for()
            enabled = page.evaluate("window.__saved")
            if not all(enabled.get(key) for key in ("enabled", "inject_state", "harvest_on_demand", "fail_closed")):
                raise RuntimeError("one-click protection did not enable state controls or changed fail-closed")
            if frame.locator("#account-mode").input_value() != "auto" or frame.locator("#state-refresh-mode").input_value() != "on_demand":
                raise RuntimeError("core default modes are incorrect")
            if frame.locator("#max-probes").input_value() != "6" or frame.locator("#pool-enabled").is_checked():
                raise RuntimeError("probe defaults or opt-in lifecycle pool are incorrect")
            frame.locator("#account-mode").select_option("custom")
            frame.locator("#state-refresh-mode").select_option("standby")
            frame.locator("#max-probes").fill("8")
            frame.locator("#pool-enabled").check()
            frame.locator("#zstd-window").fill("80")
            frame.locator("#compact-limit").fill("96")
            frame.locator("#proxy").fill("http://user:pass@127.0.0.1:10808\nsocks5h://127.0.0.1:10809\nhttp://user:pass@127.0.0.1:10808", force=True)
            frame.locator("#proxy-envs").fill("CCODEX_PROXY", force=True)
            frame.locator("#subscriptions").fill(SOURCE_URL + "\nenv:CCODEX_SUBSCRIPTION", force=True)
            frame.locator("#length").fill("292", force=True)
            frame.locator("#ttl").fill("3600", force=True)
            frame.locator("#refresh").fill("600", force=True)
            frame.locator("#cooldown").fill("180", force=True)
            frame.locator("#subscription-refresh").fill("900", force=True)
            frame.locator("#probe").fill("25", force=True)
            frame.locator("#header-timeout").fill("120", force=True)
            frame.locator("#body-limit").fill("4194304", force=True)
            frame.locator("#models").fill("gpt-6-astra\ngpt-5.6-sol", force=True)
            frame.locator("#save").click()
            frame.locator("#status").filter(has_text="配置已保存").wait_for()
            saved = page.evaluate("window.__saved")
            if not saved.get("enabled") or saved.get("proxy_urls") != ["http://user:pass@127.0.0.1:10808", "socks5h://127.0.0.1:10809"]:
                raise RuntimeError(f"unexpected saved config: {saved}")
            expected_sources = [{"url": SOURCE_URL, "user_agent": "fixture-agent", "include_protocols": ["http", "socks5"], "exclude_keywords": ["expired"]}, {"url_env": "CCODEX_SUBSCRIPTION"}]
            if saved.get("proxy_envs") != ["CCODEX_PROXY"] or saved.get("subscriptions") != expected_sources:
                raise RuntimeError(f"unexpected route sources: {saved}")
            if saved.get("accounts") != {"7": {"enabled": True}}:
                raise RuntimeError("ordinary form save erased account overrides")
            if saved.get("route_mode") != "round_robin" or saved.get("fixed_route_id") != "":
                raise RuntimeError(f"unexpected route mode: {saved}")
            if {key: saved.get(key) for key in ("account_mode", "state_refresh_mode", "max_probes_per_round", "pool_enabled", "zstd_window_mib", "compact_limit_mib", "egress_mode")} != {"account_mode":"custom", "state_refresh_mode":"standby", "max_probes_per_round":8, "pool_enabled":True, "zstd_window_mib":80, "compact_limit_mib":96, "egress_mode":"state"}:
                raise RuntimeError("core settings were not included in form save")
            if "显式出口 2 个" not in frame.locator("#route-summary").inner_text():
                raise RuntimeError("route summary did not render configured sources")
            if saved.get("models") != ["gpt-6-astra", "gpt-5.6-sol"]:
                raise RuntimeError(f"unexpected saved models: {saved.get('models')}")
            frame.locator("#fetch-nodes").click()
            frame.locator('#nodes-body tr[data-node-id="route-bbbb"]').wait_for()
            if frame.locator("#nodes-body tr[data-node-id]").count() != 2:
                raise RuntimeError("discovery did not render both node rows")
            if page.evaluate("window.__saved.route_request !== undefined"):
                raise RuntimeError("one-shot operation survived the response")
            first = frame.locator('#nodes-body tr[data-node-id="route-aaaa"]')
            first.locator('button[data-action="test"]').click()
            first.locator('td[data-state="unavailable"]').wait_for()
            if "代理认证失败" not in first.inner_text() or "12 ms" not in first.inner_text():
                raise RuntimeError("individual node test failure and latency were not displayed")
            last_action = page.evaluate("window.__calls.filter(c => c.type === 'config.save').at(-1).config.route_request")
            if last_action != {"operation": "test", "route_id": "route-aaaa"}:
                raise RuntimeError(f"per-node test did not select the requested ID: {last_action}")
            if frame.locator('#nodes-body tr[data-node-id="route-bbbb"] td[data-state="untested"]').count() != 1:
                raise RuntimeError("per-node test overwrote the other node's status")
            frame.locator('#nodes-body tr[data-node-id="route-bbbb"] button[data-action="select"]').click()
            frame.locator("#status").filter(has_text="固定节点已保存").wait_for()
            selected = page.evaluate("window.__saved")
            if selected.get("route_mode") != "fixed" or selected.get("fixed_route_id") != "route-bbbb":
                raise RuntimeError("node selection did not persist")
            if selected.get("subscriptions") != expected_sources:
                raise RuntimeError("node selection erased subscription filters or user agent")
            frame.locator("#reload").click()
            frame.locator("#status").filter(has_text="配置已加载").wait_for()
            if frame.locator("#fixed-route-id").input_value() != "route-bbbb":
                raise RuntimeError("saved fixed selection was lost after reload")
            frame.locator("#egress-mode").select_option("fixed")
            frame.locator("#egress-route").select_option("route-aaaa")
            frame.locator("#save").click()
            frame.locator("#status").filter(has_text="配置已保存").wait_for()
            if page.evaluate("window.__saved.egress_route") != "route-aaaa" or page.evaluate("window.__saved.fixed_route_id") != "route-bbbb":
                raise RuntimeError("formal egress selection did not remain independent from state collector")
            frame.locator("#test-nodes").click()
            frame.locator('#nodes-body tr[data-node-id="route-bbbb"] td[data-state="restricted"]').wait_for()
            if "HTTP 403" not in frame.locator('#nodes-body tr[data-node-id="route-bbbb"]').inner_text():
                raise RuntimeError("restricted response was incorrectly hidden or shown as a generic failure")

            # Core status never fabricates a ready session and never overwrites
            # form edits while fetching a runtime snapshot.
            frame.locator("#max-probes").fill("10")
            page.evaluate("window.__replyDelay = 200")
            frame.locator("#refresh-core").click()
            frame.locator("#max-probes").fill("11")
            frame.locator('#sessions-body tr[data-session-id="session-a"]').wait_for()
            page.evaluate("window.__replyDelay = 0")
            if frame.locator("#max-probes").input_value() != "11" or page.evaluate("window.__saved.max_probes_per_round") != 8:
                raise RuntimeError("status refresh saved or discarded in-progress form input")
            session = frame.locator('#sessions-body tr[data-session-id="session-a"]')
            if "state 可用" not in session.inner_text() or "292 / 实测 292" not in session.inner_text():
                raise RuntimeError("key header state lengths/readiness were not rendered")
            if "快照时间" not in frame.locator("#core-snapshot").inner_text():
                raise RuntimeError("state report lacks an explicit snapshot timestamp")
            if not frame.locator('#sessions-body tr[data-session-id="session-b"] button').is_disabled() or not frame.locator('#sessions-body tr[data-session-id="session-c"] button').is_disabled():
                raise RuntimeError("retry remained enabled during cooldown or collection")
            frame.locator("#retry-route").select_option("route-bbbb")
            session.locator("button").click()
            frame.locator("#status").filter(has_text="采集请求已提交").wait_for()
            if page.evaluate("window.__calls.at(-1).config.core_request") != {"operation":"retry", "session_id":"session-a", "route_id":"route-bbbb"}:
                raise RuntimeError("manual state collection did not target its live session and selected node")
            if frame.locator("#retry-route").input_value() != "route-bbbb" or frame.locator("#max-probes").input_value() != "11":
                raise RuntimeError("manual state collection discarded selected node or unsaved form input")
            frame.locator("#verify-all-states").click()
            mismatch = frame.locator('#verification-body tr[data-verification-id="route-aaaa"]')
            mismatch.filter(has_text="不符合当前规则").wait_for()
            if "已收到 · 可解析" not in mismatch.inner_text() or "312 字符 / 11 块" not in mismatch.inner_text() or "目标 292 字符 / 10 块" not in mismatch.inner_text():
                raise RuntimeError("node qualification did not distinguish parsed wrong-shape header from connectivity")
            if page.evaluate("window.__calls.at(-1).config.core_request") != {"operation":"verify_nodes","session_id":"session-a"}:
                raise RuntimeError("all-node state verification was replaced by a connectivity request")
            if not frame.locator("#verify-all-states").is_disabled() or frame.locator("#cancel-verification").is_disabled():
                raise RuntimeError("active verification start/cancel controls are incorrect")
            frame.locator("#cancel-verification").click()
            frame.locator("#verification-status").filter(has_text="已取消").wait_for()
            frame.locator("#verify-one-state").click()
            frame.locator('#verification-body tr[data-verification-id="route-bbbb"]').filter(has_text="已取得合格 state").wait_for()
            if page.evaluate("window.__calls.at(-1).config.core_request") != {"operation":"verify_nodes","session_id":"session-a","route_ids":["route-bbbb"]}:
                raise RuntimeError("single-node qualification used wrong route")
            if frame.locator("#max-probes").input_value() != "11":
                raise RuntimeError("qualification polling erased unsaved inputs")
            frame.locator("#state-verification").scroll_into_view_if_needed()
            page.screenshot(path=str(ROOT / ".build" / "ui-state-qualification.png"))
            pool = frame.locator('#pool-body tr[data-pool-id="route-aaaa"]')
            pool.locator('button[data-pool-action="available"]').click()
            frame.locator("#status").filter(has_text="节点状态已更新").wait_for()
            if page.evaluate("window.__calls.at(-1).config.core_request") != {"operation":"pool", "route_ids":["route-aaaa"], "action":"available"}:
                raise RuntimeError("node recycle contract is incorrect")
            if not pool.locator('button[data-pool-action="available"]').is_disabled():
                raise RuntimeError("available node recycle button remained enabled")
            pool.locator('button[data-pool-action="disabled"]').click()
            pool.filter(has_text="已停用").wait_for()
            page.evaluate("window.__noSessions = true")
            frame.locator("#refresh-core").click()
            frame.locator("#sessions-body").filter(has_text="暂无运行会话").wait_for()
            if not frame.locator("#verify-all-states").is_disabled():
                raise RuntimeError("verification could start without an authenticated live session")
            if frame.locator("#sessions-body button").count() or not frame.locator("#retry-route").is_disabled():
                raise RuntimeError("no-session report offered a fake collection action")
            page.evaluate("window.__noSessions = false")
            frame.locator("#refresh-core").click()
            frame.locator('#sessions-body tr[data-session-id="session-a"]').wait_for()
            frame.locator("#body-limit").fill("134217728")
            frame.locator("#save").click()
            frame.locator("#status").filter(has_text="配置已保存").wait_for()
            if page.evaluate("window.__saved.max_body_bytes") != 134217728:
                raise RuntimeError("128 MiB body limit was rejected")
            if page.evaluate("window.__saved.core_report !== undefined") or frame.locator("#sessions-body button").count():
                raise RuntimeError("saving policy left an obsolete ready snapshot displayed")
            frame.locator("#allow-passthrough").click()
            frame.locator("#status").filter(has_text="兼容转发已开启").wait_for()
            if page.evaluate("window.__saved.fail_closed") or not page.evaluate("window.__saved.inject_state"):
                raise RuntimeError("compatibility action failed to preserve state enhancement while enabling fallback")
            frame.locator("#refresh-core").click()
            frame.locator('#sessions-body tr[data-session-id="session-a"]').wait_for()

            # An intentionally saved invalid source receives a diagnostic error;
            # source settings remain visible so the user can correct them.
            invalid_url = "https://example.test/expired?filter=x,y"
            frame.locator("#subscriptions").fill(invalid_url)
            page.evaluate("window.__nextError = 'SUBSCRIPTION_HTTP_ERROR'")
            frame.locator("#fetch-nodes").click()
            frame.locator("#nodes-status").filter(has_text="订阅服务器拒绝请求").wait_for()
            if frame.locator("#subscriptions").input_value() != invalid_url:
                raise RuntimeError("failed source discovery silently cleared the user's address")
            if page.evaluate("window.__saved.proxy_urls[0]") != "http://user:pass@127.0.0.1:10808":
                raise RuntimeError("subscription failure erased the existing authenticated proxy")
            if frame.locator("#status").get_attribute("data-error") != "true":
                raise RuntimeError("subscription failure was presented as success")

            # A failed initial/renewed config read may not enable destructive saves.
            page.evaluate("window.__nextLoadError = true")
            frame.locator("#reload").click()
            frame.locator("#status").filter(has_text="原配置尚未加载").wait_for()
            if not frame.locator("#save").is_disabled() or not frame.locator("#fetch-nodes").is_disabled():
                raise RuntimeError("failed config load left write actions enabled")
            frame.locator("#reload").click()
            frame.locator("#status").filter(has_text="配置已加载").wait_for()
            frame.locator("#fetch-nodes").click()
            frame.locator('#nodes-body tr[data-node-id="route-bbbb"]').wait_for()

            resize = page.evaluate("window.__resize")
            if not isinstance(resize, int) or resize <= 0:
                raise RuntimeError("UI did not publish a positive resize height")
            page.set_viewport_size({"width": 390, "height": 844})
            frame.locator("#nodes").scroll_into_view_if_needed()
            dimensions = frame.evaluate("({width:document.documentElement.clientWidth,scroll:document.documentElement.scrollWidth})")
            if dimensions["scroll"] > dimensions["width"] + 1:
                raise RuntimeError(f"phone layout overflows horizontally: {dimensions}")
            page.set_viewport_size({"width": 1200, "height": 1000})
            output = ROOT / ".build" / "ui-preview.png"
            output.parent.mkdir(parents=True, exist_ok=True)
            frame.locator("#policy").scroll_into_view_if_needed()
            page.screenshot(path=str(ROOT / ".build" / "ui-core-preview.png"))
            frame.locator("#nodes").scroll_into_view_if_needed()
            page.screenshot(path=str(output))
            calls = page.evaluate("window.__calls.map(c => c.type)")
            if set(calls) != {"config.load", "config.save"}:
                raise RuntimeError(f"UI called an unsupported bridge method: {calls}")
            browser.close()
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
    if errors:
        raise RuntimeError(f"page errors: {errors}")
    print(json.dumps({"passed": True, "fixture": "UI bridge contract", "flows": ["initial_load", "enable_protection", "core_options", "core_snapshot", "state_retry", "node_pool", "unsaved_input_preserved", "authenticated_proxy", "subscription_query_and_filters", "discover", "single_test", "select_and_reload", "independent_egress", "all_test", "source_error", "load_failure", "phone_layout", "resize"], "page_errors": errors}, ensure_ascii=False))


if __name__ == "__main__":
    main()
