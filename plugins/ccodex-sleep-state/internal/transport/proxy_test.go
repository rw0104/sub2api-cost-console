package transport

import (
	"net/http"
	"net/url"
	"testing"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestBuildTransportAcceptsSocks5WithoutDialing(t *testing.T) {
	tr, err := buildTransport("socks5h://127.0.0.1:1080", 10)
	if err != nil {
		t.Fatal(err)
	}
	tr.CloseIdleConnections()
}

func TestBuildTransportAcceptsAuthenticatedHTTPProxy(t *testing.T) {
	tr, err := buildTransport("http://user:pass@127.0.0.1:8080", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.CloseIdleConnections()
	proxyURL, err := tr.Proxy(&http.Request{URL: mustURL(t, "https://example.test/")})
	if err != nil || proxyURL == nil || proxyURL.User == nil {
		t.Fatalf("proxy URL = %#v, err=%v", proxyURL, err)
	}
	password, _ := proxyURL.User.Password()
	if proxyURL.User.Username() != "user" || password != "pass" {
		t.Fatalf("proxy credentials were not preserved: %v", proxyURL.User)
	}
}
