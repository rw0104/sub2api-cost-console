package transport

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/metacubex/mihomo/component/auth"
	"github.com/metacubex/mihomo/transport/socks5"
	"local.sub2api/ccodex-sleep-state/internal/config"
	"local.sub2api/ccodex-sleep-state/internal/turnstate"
)

// This is a real local CONNECT tunnel, not a mocked RoundTripper: both the
// production Mihomo dialer and the standard transport have to negotiate it.
func localCONNECTProxy(t *testing.T, upstream, authority, expectedAuth string, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	var tunnels sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodConnect || r.Host != authority {
			t.Errorf("unexpected proxy request: method=%s authority=%s", r.Method, r.Host)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("upstream authorization leaked to proxy CONNECT headers")
		}
		if r.Header.Get("Proxy-Authorization") != expectedAuth {
			w.Header().Set("Proxy-Authenticate", `Basic realm="local-test"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		remote, err := net.DialTimeout("tcp", upstream, time.Second)
		if err != nil {
			t.Error("cannot reach local origin")
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			remote.Close()
			t.Error(err)
			return
		}
		tunnels.Add(1)
		defer tunnels.Done()
		defer client.Close()
		defer remote.Close()
		_ = client.SetDeadline(time.Now().Add(5 * time.Second))
		_ = remote.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffered.Flush()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(remote, buffered); _ = remote.Close(); close(done) }()
		_, _ = io.Copy(client, remote)
		_ = client.Close()
		<-done
	}))
	t.Cleanup(func() { server.Close(); tunnels.Wait() })
	return server
}

func TestConfiguredLoopbackSOCKSUsesRemoteDNS(t *testing.T) {
	var originCalls, proxyCalls atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("SOCKS authentication leaked to origin")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer origin.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var tunnels sync.WaitGroup
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			tunnels.Add(1)
			go func() {
				defer tunnels.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				addr, command, _, err := socks5.ServerHandshake(conn, auth.NewAuthenticator([]auth.AuthUser{{User: "local", Pass: "password"}}))
				if err != nil {
					return
				}
				if command != socks5.CmdConnect || addr.String() != "example.com:443" || addr[0] != socks5.AtypDomainName {
					t.Error("SOCKS target did not preserve the remote DNS name")
					return
				}
				proxyCalls.Add(1)
				remote, err := net.DialTimeout("tcp", origin.Listener.Addr().String(), time.Second)
				if err != nil {
					t.Error(err)
					return
				}
				defer remote.Close()
				_ = remote.SetDeadline(time.Now().Add(5 * time.Second))
				done := make(chan struct{})
				go func() { _, _ = io.Copy(remote, conn); _ = remote.Close(); close(done) }()
				_, _ = io.Copy(conn, remote)
				_ = conn.Close()
				<-done
			}()
		}
	}()
	defer func() { listener.Close(); <-stopped; tunnels.Wait() }()
	for _, scheme := range []string{"socks5", "socks5h"} {
		raw, _ := json.Marshal(map[string]any{"proxy_urls": []string{scheme + "://local:password@" + listener.Addr().String()}})
		c, err := config.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		registry, err := NewRegistryFromConfig(context.Background(), c, "")
		if err != nil {
			t.Fatal(err)
		}
		route, _ := registry.At(0)
		roots := x509.NewCertPool()
		roots.AddCert(origin.Certificate())
		route.Transport.TLSClientConfig.RootCAs = roots
		result := checkRouteConnectivity(context.Background(), route, "https://example.com/", 2*time.Second)
		registry.Close()
		if result.Status != "available" || result.HTTPStatus != 200 {
			t.Fatalf("%s local SOCKS result=%+v", scheme, result)
		}
	}
	if proxyCalls.Load() != 2 || originCalls.Load() != 2 {
		t.Fatalf("SOCKS tunnels=%d origin calls=%d", proxyCalls.Load(), originCalls.Load())
	}
}

func TestConfiguredLoopbackProxyForwardsProbeAndGeneration(t *testing.T) {
	for _, test := range []struct {
		name, user, password string
		reject               bool
	}{
		{name: "plain"},
		{name: "authenticated", user: "local-user", password: "p@ss:word/%"},
		{name: "rejected", user: "local-user", password: "wrong", reject: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := validTurnState('p')
			var originCalls, proxyCalls atomic.Int32
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := originCalls.Add(1)
				if r.Header.Get("Proxy-Authorization") != "" {
					t.Error("proxy credentials leaked to origin")
				}
				if r.Header.Get("Authorization") != "Bearer local-origin-token" {
					t.Error("origin authorization was lost")
				}
				if call == 1 && r.Header.Get(turnstate.Header) != "" {
					t.Error("probe contains previous turn state")
				}
				if call == 2 && r.Header.Get(turnstate.Header) != state {
					t.Error("generation did not use probed state")
				}
				w.Header().Set(turnstate.Header, state)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
			}))
			defer origin.Close()
			// example.com is in the httptest certificate. The proxy resolves it
			// to the local server; the plugin must not resolve or dial it directly.
			authority := "example.com:443"
			wantedAuth := ""
			if test.user != "" {
				wantedAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(test.user+":"+test.password))
			}
			if test.reject {
				wantedAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte("local-user:correct"))
			}
			proxy := localCONNECTProxy(t, origin.Listener.Addr().String(), authority, wantedAuth, &proxyCalls)
			proxyURL, _ := url.Parse(proxy.URL)
			if test.user != "" {
				proxyURL.User = url.UserPassword(test.user, test.password)
			}
			raw, _ := json.Marshal(map[string]any{"proxy_urls": []string{proxyURL.String()}, "enabled": true, "inject_state": true, "harvest_on_demand": true, "fail_closed": true, "state_target_length": len(state), "models": []string{"gpt-6-astra"}})
			c, err := config.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			registry, err := NewRegistryFromConfig(ctx, c, "")
			if err != nil {
				t.Fatal(err)
			}
			defer registry.Close()
			route, _ := registry.At(0)
			roots := x509.NewCertPool()
			roots.AddCert(origin.Certificate())
			route.Transport.TLSClientConfig.RootCAs = roots
			handler := &Handler{Config: func() *config.Config { return c }, States: turnstate.New(), RegistryContext: func(context.Context, *config.Config, string) (*Registry, error) { return registry, nil }}
			stream := routeRequestStream(900)
			stream.ctx = ctx
			start := stream.requests[0].GetStart()
			start.Url, start.Host = "https://"+authority+"/responses", "example.com"
			start.Headers = map[string]*v1.HeaderValues{"Authorization": {Values: []string{"Bearer local-origin-token"}}, "Proxy-Authorization": {Values: []string{"must-not-leak"}}}
			if err := handler.Forward(stream); err != nil {
				t.Fatal(err)
			}
			if test.reject {
				if originCalls.Load() != 0 {
					t.Fatal("authentication rejection bypassed proxy")
				}
				if len(stream.responses) != 1 || stream.responses[0].GetError() == nil || stream.responses[0].GetError().Code != "STATE_UNAVAILABLE" {
					t.Fatalf("rejected proxy response: %v", stream.responses)
				}
				checked := checkRouteConnectivity(ctx, route, "https://"+authority+"/", time.Second)
				if checked.ErrorCode != "proxy_auth" {
					t.Fatalf("proxy rejection diagnostic = %+v", checked)
				}
				return
			}
			if originCalls.Load() != 2 || proxyCalls.Load() == 0 {
				t.Fatalf("origin=%d proxy=%d; want probe and generation through CONNECT", originCalls.Load(), proxyCalls.Load())
			}
			if len(stream.responses) < 2 || stream.responses[0].GetStart() == nil || stream.responses[0].GetStart().StatusCode != 200 {
				t.Fatalf("forward failed: %v", stream.responses)
			}
		})
	}
}

func TestStandardLoopbackAuthenticatedCONNECT(t *testing.T) {
	var originCalls, proxyCalls atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials reached origin")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer origin.Close()
	authority := strings.TrimPrefix(origin.URL, "https://")
	proxy := localCONNECTProxy(t, authority, authority, "Basic "+base64.StdEncoding.EncodeToString([]byte("test:secret")), &proxyCalls)
	u, _ := url.Parse(proxy.URL)
	u.User = url.UserPassword("test", "secret")
	tr, err := buildTransport(u.String(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.CloseIdleConnections()
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	tr.TLSClientConfig.RootCAs = roots
	response, err := (&http.Client{Transport: tr, Timeout: 3 * time.Second}).Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || proxyCalls.Load() != 1 || originCalls.Load() != 1 {
		t.Fatalf("status=%d proxy=%d origin=%d", response.StatusCode, proxyCalls.Load(), originCalls.Load())
	}
}
