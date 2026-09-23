package mihomoroute

import (
	"encoding/base64"
	"strconv"
	"testing"
)

func TestHTTPDefaultPortsPreserveRouteIdentity(t *testing.T) {
	for scheme, port := range map[string]int{"http": 80, "https": 443} {
		for _, host := range []string{"127.0.0.1", "[::1]", "user:password@127.0.0.1"} {
			short, err := ParseURI(scheme + "://" + host)
			if err != nil {
				t.Fatal(err)
			}
			explicit, err := ParseURI(scheme + "://" + host + ":" + strconv.Itoa(port))
			if err != nil {
				t.Fatal(err)
			}
			shortID, _ := nodeIdentity(short)
			explicitID, _ := nodeIdentity(explicit)
			if short["port"] != port || shortID != explicitID {
				t.Fatalf("%s default port did not preserve node identity", scheme)
			}
		}
	}
	for _, raw := range []string{"socks5://127.0.0.1", "socks5h://127.0.0.1", "http://127.0.0.1:", "https://127.0.0.1:0", "http://127.0.0.1:65536"} {
		if _, err := ParseURI(raw); err == nil {
			t.Fatalf("invalid/missing port accepted: %s", raw)
		}
	}
}

func TestParseBase64VLESSSubscriptionAndBuildRoute(t *testing.T) {
	uri := "vless://5802a429-459b-43ac-823a-8bd7c8b5b71b@example.com:443?type=tcp&encryption=none&security=reality&flow=xtls-rprx-vision&fp=chrome&sni=example.com&pbk=rBLgYxDPw9nmzppUc_8Hr-puEW7lkpN1zoRDRcA-Ywo&sid=5f7b#test"
	encoded := base64.StdEncoding.EncodeToString([]byte(uri + "\n"))
	nodes, err := Parse([]byte(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0]["type"] != "vless" {
		t.Fatalf("unexpected nodes: %#v", nodes)
	}
	route, err := Build(nodes[0], 0)
	if err != nil {
		t.Fatal(err)
	}
	defer route.Close()
	if route.ID == "" || route.Protocol != "vless" || route.Transport == nil {
		t.Fatalf("unexpected route: %+v", route)
	}
}

func TestBuildPreservesSanitizedDisplayName(t *testing.T) {
	for _, test := range []struct{ name, want string }{
		{"Tokyo stable", "Tokyo stable"},
		{"HTTP proxy http://user:secret@proxy.example:80", "http 线路"},
		{"node secret", "http 线路"},
		{"name\n\u202E", "name"},
	} {
		route, err := Build(map[string]any{"name": test.name, "type": "http", "server": "proxy.example", "port": 80, "username": "user", "password": "secret"}, 2)
		if err != nil {
			t.Fatal(err)
		}
		route.Close()
		if route.DisplayName != test.want {
			t.Fatalf("name=%q want %q", route.DisplayName, test.want)
		}
	}
}
