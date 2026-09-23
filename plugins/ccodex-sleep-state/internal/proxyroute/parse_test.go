package proxyroute

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseProxyYAMLAndDeduplicates(t *testing.T) {
	nodes, err := Parse([]byte(`proxies:
  - name: one
    type: http
    server: 127.0.0.1
    port: 18080
  - name: duplicate
    type: http
    server: 127.0.0.1
    port: 18080
  - name: two
    type: socks5
    server: 127.0.0.1
    port: 18081
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Scheme != "http" || nodes[1].Scheme != "socks5" {
		t.Fatalf("unexpected nodes: %#v", nodes)
	}
}

func TestParseURIAndBase64(t *testing.T) {
	raw := "http://127.0.0.1:18080\nsocks5h://user:pass@127.0.0.1:18081"
	encoded := base64.RawStdEncoding.EncodeToString([]byte(raw))
	nodes, err := Parse([]byte(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[1].Username != "user" || nodes[1].Password != "pass" || nodes[1].Scheme != "socks5h" {
		t.Fatalf("unexpected decoded nodes: %#v", nodes)
	}
	if got, err := nodes[0].URL(); err != nil || got != "http://127.0.0.1:18080" {
		t.Fatalf("node URL = %q, err=%v", got, err)
	}
}

func TestParseRejectsUnsafeOrUnsupportedNodes(t *testing.T) {
	unsafe := []byte(`proxies:
  - type: http
    server: 127.0.0.1
    port: 18080
    skip-cert-verify: true
`)
	if _, err := Parse(unsafe); err == nil || !strings.Contains(err.Error(), "insecure TLS") {
		t.Fatalf("unsafe node error = %v", err)
	}
	unsupported := []byte(`proxies:
  - type: vmess
    server: 127.0.0.1
    port: 18080
`)
	if _, err := Parse(unsupported); err == nil {
		t.Fatal("unsupported Mihomo protocol must be rejected in the standard transport slice")
	}
}

func TestParseRejectsOversizeSubscription(t *testing.T) {
	if _, err := Parse(make([]byte, MaxSubscriptionBytes+1)); err == nil {
		t.Fatal("oversize subscription must be rejected")
	}
}
