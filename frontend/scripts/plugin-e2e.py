"""Real-browser companion to go test -tags plugin_e2e ./cmd/server.

Requires Python Playwright + installed Chromium. All accounts and secrets come
from the temporary Go fixture; do not point this script at a production server.
Run with --fixture PATH --url http://127.0.0.1:VITE_PORT --output PATH.
"""
import argparse
import base64
import hashlib
import hmac
import json
from pathlib import Path
import re
import struct
import time
from urllib.parse import urlparse

from playwright.sync_api import sync_playwright, expect


def otp(secret):
    digest = hmac.new(base64.b32decode(secret), struct.pack(">Q", int(time.time()) // 30), hashlib.sha1).digest()
    offset = digest[-1] & 15
    return f"{(struct.unpack('>I', digest[offset:offset + 4])[0] & 0x7fffffff) % 1000000:06}"


def main():
    expect.set_options(timeout=30000)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fixture", required=True)
    parser.add_argument("--url", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    fixture = json.loads(Path(args.fixture).read_text(encoding="utf-8"))
    for value in (args.url, fixture["backend_url"]):
        assert urlparse(value).hostname in ("127.0.0.1", "localhost"), "Only isolated localhost fixtures are allowed"
    out = Path(args.output)
    out.mkdir(parents=True, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch()
        context = browser.new_context(viewport={"width": 1440, "height": 1080}, locale="en-US")
        page = context.new_page()
        page.set_default_timeout(30000)
        page.add_init_script("if (window === window.top) localStorage.setItem('sub2api_locale', 'en')")
        page.on("dialog", lambda dialog: dialog.accept())
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        network_failures = []
        page.on("requestfailed", lambda request: network_failures.append({"path": urlparse(request.url).path, "error": request.failure}))
        evidence = []

        def snapshot(name):
            # Inspect the accessible tree before acting and preserve redacted
            # evidence (no login credentials or temporary tokens in artifacts).
            tree = page.locator("body").aria_snapshot()
            for secret in (fixture["email"], fixture["password"], fixture["totp_secret"], fixture["api_key"]):
                tree = tree.replace(secret, "[fixture]")
            (out / (name + ".txt")).write_text(tree, encoding="utf-8")
            page.screenshot(path=str(out / (name + ".png")), full_page=True)
            evidence.append(name)

        def enter_otp():
            cells = page.locator('input[maxlength="1"][inputmode="numeric"]:visible')
            expect(cells).to_have_count(6, timeout=30000)
            for index, digit in enumerate(otp(fixture["totp_secret"])):
                cells.nth(index).fill(digit)
            expect(cells).to_have_count(0)

        def response_action(suffix, action, method="POST"):
            with page.expect_response(lambda r: urlparse(r.url).path.endswith(suffix) and r.request.method == method and r.status < 400) as pending:
                action()
            data = pending.value.json()
            return data.get("data", data)

        try:
            page.goto(args.url + "/login", wait_until="domcontentloaded", timeout=90000)
            expect(page.locator("#email")).to_be_visible(timeout=90000)
            snapshot("01-login")
            page.locator("#email").fill(fixture["email"])
            page.locator("#password").fill(fixture["password"])
            page.get_by_role("button", name="Sign In", exact=True).click()
            enter_otp()
            page.wait_for_url(re.compile(r"/(dashboard|admin/)"))
            page.goto(args.url + "/admin/plugins", wait_until="domcontentloaded", timeout=90000)
            card = page.locator("article").filter(has_text="example.request-policy")
            expect(card).to_have_count(1)
            welcome = page.get_by_role("dialog", name=re.compile("Welcome to Sub2API"))
            expect(welcome).to_be_visible(timeout=15000)
            welcome.get_by_role("button", name="Close", exact=True).click()
            snapshot("02-installed")

            # The API fixture leaves an installed but disabled version. Reinstall
            # through the visible upload control to cover UI -> multipart -> DB.
            page.locator('input[type="file"]').first.set_input_files(fixture["packages"]["0.1.0"])
            enter_otp()  # fresh browser session has no step-up grant
            expect(page.get_by_text("First installation from this publisher", exact=True)).to_be_visible()
            expect(page.get_by_test_id("confirm-publisher-install")).to_be_disabled()
            snapshot("03-publisher-review")
            page.get_by_test_id("publisher-consent").check()
            response_action("/upload", lambda: page.get_by_test_id("confirm-publisher-install").click())
            expect(page.get_by_text("Plugin installed and kept disabled", exact=True)).to_be_visible()
            response_action("/enable", lambda: card.get_by_role("button", name="Enable", exact=True).click())
            expect(card.get_by_text("Enabled", exact=True)).to_be_visible()

            card.get_by_role("button", name="Configure", exact=True).click()
            frame = page.frame_locator("iframe")
            expect(frame.get_by_role("status")).to_have_text("Ready")
            snapshot("03-configuration")
            frame.get_by_label("Maximum output tokens").fill("17")
            frame.get_by_role("button", name="Save and test", exact=True).click()
            expect(frame.get_by_role("status")).to_have_text("Configuration test passed")
            page.get_by_role("button", name="Close modal", exact=True).click()

            card.get_by_role("button", name="Details & management", exact=True).click()
            expect(card.get_by_text("Healthy", exact=True).first).to_be_visible()
            card.get_by_role("button", name="Routing policy", exact=True).click()
            dialog = page.get_by_role("dialog")
            snapshot("04-routing")
            dialog.get_by_label("Priority", exact=True).fill("10")
            dialog.get_by_label("Account IDs", exact=True).fill(str(fixture["account_id"]))
            dialog.get_by_label("User IDs", exact=True).fill(str(fixture["admin_id"]))
            dialog.get_by_label("Authenticated group IDs", exact=True).fill(str(fixture["group_id"]))
            saved = response_action("/routing", lambda: dialog.get_by_role("button", name="Save", exact=True).click(), "PUT")
            assert saved["bindings"][0]["account_ids"] == [fixture["account_id"]]
            assert saved["bindings"][0]["priority"] == 10
            expect(dialog).to_have_count(0)

            # Upgrade through the real file chooser, then restore version/config.
            with page.expect_file_chooser() as chooser:
                card.get_by_role("button", name="Upgrade", exact=True).click()
            upgraded = response_action("/upgrade", lambda: chooser.value.set_files(fixture["packages"]["0.1.1"]))
            assert upgraded["runtime_version"] == "0.1.1"
            card.get_by_role("button", name="Version history", exact=True).click()
            dialog = page.get_by_role("dialog")
            expect(dialog.get_by_role("button", name="Roll back", exact=True).first).to_be_visible()
            snapshot("05-rollback")
            rolled = response_action("/rollback", lambda: dialog.get_by_role("button", name="Roll back", exact=True).first.click())
            assert rolled["runtime_version"] == "0.1.0"
            response_action("/disable", lambda: card.get_by_role("button", name="Disable", exact=True).click())
            snapshot("06-complete")
            assert not errors, errors
            (out / "result.json").write_text(json.dumps({"passed": True, "checks": ["login_totp", "install_stepup", "first_publisher_consent", "compiled_package_install", "remembered_publisher_upgrade", "enable", "iframe_config_test", "routing_cas", "hot_upgrade", "rollback", "disable"], "snapshots": evidence, "page_errors": errors}, indent=2), encoding="utf-8")
            done = Path(fixture["done_file"])
            pending = done.with_suffix('.tmp')
            pending.write_text('{"passed":true}', encoding="utf-8")
            pending.replace(done)
            print("PASS: real browser plugin lifecycle; evidence:", out)
        except Exception:
            print("Network failures:", network_failures)
            snapshot("failure")
            raise
        finally:
            context.close()
            browser.close()


if __name__ == "__main__":
    main()
