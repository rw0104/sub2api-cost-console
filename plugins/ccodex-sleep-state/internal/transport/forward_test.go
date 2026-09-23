package transport

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"google.golang.org/grpc/metadata"
	"local.sub2api/ccodex-sleep-state/internal/config"
	"local.sub2api/ccodex-sleep-state/internal/turnstate"
)

type fakeStream struct {
	ctx       context.Context
	requests  []*v1.ForwardRequest
	responses []*v1.ForwardResponse
	index     int
}

func (s *fakeStream) Recv() (*v1.ForwardRequest, error) {
	if s.index >= len(s.requests) {
		return nil, io.EOF
	}
	request := s.requests[s.index]
	s.index++
	return request, nil
}

func (s *fakeStream) Send(response *v1.ForwardResponse) error {
	s.responses = append(s.responses, response)
	return nil
}

func (s *fakeStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeStream) SetTrailer(metadata.MD)       {}
func (s *fakeStream) Context() context.Context     { return s.ctx }
func (s *fakeStream) SendMsg(any) error            { return nil }
func (s *fakeStream) RecvMsg(any) error            { return nil }

func validTurnState(fill byte) string {
	raw := make([]byte, 73)
	raw[0] = 0x80
	binary.BigEndian.PutUint64(raw[1:9], uint64(time.Now().Add(-time.Minute).Unix()))
	for index := 9; index < len(raw); index++ {
		raw[index] = fill
	}
	return base64.URLEncoding.EncodeToString(raw)
}

func TestForwardInjectsStateAndStreamsResponse(t *testing.T) {
	state := validTurnState('s')
	var receivedState string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedState = r.Header.Get(turnstate.Header)
		w.Header().Set(turnstate.Header, state)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: response.completed\n\n"))
	}))
	defer server.Close()

	c, err := config.Parse([]byte(fmt.Sprintf(`{"enabled":true,"inject_state":true,"harvest_on_demand":false,"state_target_length":%d,"models":["gpt-6-astra"]}`, len(state))))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled || !c.InjectState {
		t.Fatalf("unexpected config: %+v", c)
	}
	store := turnstate.New()
	policy, _ := policyForConfig(c)
	if !store.Offer(7, "gpt-6-astra", state, 0, policy, time.Now()) {
		t.Fatal("failed to seed state")
	}
	upstreamTransport := server.Client().Transport.(*http.Transport).Clone()
	upstreamTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // local httptest certificate only

	payload := []byte(`{"model":"gpt-6-astra","input":[]}`)
	stream := &fakeStream{
		ctx: context.Background(),
		requests: []*v1.ForwardRequest{
			{Frame: &v1.ForwardRequest_Start{Start: &v1.ForwardRequestStart{
				RequestId: "request-1", Method: http.MethodPost, Url: server.URL + "/responses", Host: strings.TrimPrefix(server.URL, "https://"),
				Headers: map[string]*v1.HeaderValues{"content-type": {Values: []string{"application/json"}}}, AccountId: 7,
				Platform: "openai", AccountType: "oauth", ContentLength: int64(len(payload)), HasBody: true,
			}}},
			{Frame: &v1.ForwardRequest_BodyChunk{BodyChunk: payload}},
			{Frame: &v1.ForwardRequest_BodyEnd{BodyEnd: true}},
		},
	}
	handler := &Handler{
		Config:           func() *config.Config { return c },
		States:           store,
		TransportFactory: func(string, int) (*http.Transport, error) { return upstreamTransport.Clone(), nil },
	}
	if err := handler.Forward(stream); err != nil {
		t.Fatal(err)
	}
	if receivedState != state {
		t.Fatalf("upstream state = %q, want %q", receivedState, state)
	}
	if len(stream.responses) != 3 {
		t.Fatalf("response frame count = %d, want 3", len(stream.responses))
	}
	if start := stream.responses[0].GetStart(); start == nil || start.StatusCode != http.StatusOK {
		t.Fatalf("unexpected response start: %+v", stream.responses[0])
	}
	if got := string(stream.responses[1].GetBodyChunk()); got != "data: response.completed\n\n" {
		t.Fatalf("response body = %q", got)
	}
	if end := stream.responses[2].GetEnd(); end == nil || end.BytesReceived == 0 {
		t.Fatalf("unexpected response end: %+v", stream.responses[2])
	}
	if got, ok := store.Acquire(7, "gpt-6-astra", policy, time.Now()); !ok || got.Token.Value != state {
		t.Fatalf("response state was not retained: %+v %v", got, ok)
	}
}

func TestForwardFailsClosedBeforeUpstreamWhenStateMissing(t *testing.T) {
	called := false
	c, err := config.Parse([]byte(`{"enabled":true,"inject_state":true,"harvest_on_demand":false,"fail_closed":true,"state_target_length":100,"models":["gpt-6-astra"]}`))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"model":"gpt-6-astra","input":[]}`)
	stream := &fakeStream{ctx: context.Background(), requests: []*v1.ForwardRequest{
		{Frame: &v1.ForwardRequest_Start{Start: &v1.ForwardRequestStart{Method: http.MethodPost, Url: "https://example.test/responses", AccountId: 7, Platform: "openai", AccountType: "oauth", ContentLength: int64(len(payload)), HasBody: true}}},
		{Frame: &v1.ForwardRequest_BodyChunk{BodyChunk: payload}},
		{Frame: &v1.ForwardRequest_BodyEnd{BodyEnd: true}},
	}}
	handler := &Handler{
		Config: func() *config.Config { return c },
		States: turnstate.New(),
		TransportFactory: func(string, int) (*http.Transport, error) {
			called = true
			return nil, errors.New("must not dial")
		},
	}
	if err := handler.Forward(stream); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("upstream must not be called when fail closed")
	}
	if len(stream.responses) != 1 || stream.responses[0].GetError().Code != "STATE_UNAVAILABLE" {
		t.Fatalf("unexpected failure response: %+v", stream.responses)
	}
}

func TestForwardHarvestsStateBeforeFormalRequest(t *testing.T) {
	state := validTurnState('h')
	var calls int
	var formalHeader string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		w.Header().Set(turnstate.Header, state)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if strings.Contains(string(body), "Reply with OK.") {
			_, _ = w.Write([]byte("data: response.completed\n\n"))
			return
		}
		formalHeader = r.Header.Get(turnstate.Header)
		_, _ = w.Write([]byte("data: response.completed\n\n"))
	}))
	defer server.Close()
	c, err := config.Parse([]byte(fmt.Sprintf(`{"enabled":true,"inject_state":true,"harvest_on_demand":true,"fail_closed":true,"state_target_length":%d,"models":["gpt-6-astra"]}`, len(state))))
	if err != nil {
		t.Fatal(err)
	}
	upstreamTransport := server.Client().Transport.(*http.Transport).Clone()
	upstreamTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // local httptest certificate only
	payload := []byte(`{"model":"gpt-6-astra","input":[]}`)
	stream := &fakeStream{ctx: context.Background(), requests: []*v1.ForwardRequest{
		{Frame: &v1.ForwardRequest_Start{Start: &v1.ForwardRequestStart{Method: http.MethodPost, Url: server.URL + "/responses", Host: strings.TrimPrefix(server.URL, "https://"), AccountId: 9, Platform: "openai", AccountType: "oauth", ContentLength: int64(len(payload)), HasBody: true}}},
		{Frame: &v1.ForwardRequest_BodyChunk{BodyChunk: payload}},
		{Frame: &v1.ForwardRequest_BodyEnd{BodyEnd: true}},
	}}
	handler := &Handler{
		Config:           func() *config.Config { return c },
		States:           turnstate.New(),
		TransportFactory: func(string, int) (*http.Transport, error) { return upstreamTransport.Clone(), nil },
	}
	if err := handler.Forward(stream); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || formalHeader != state {
		t.Fatalf("probe/formal flow = calls %d, formal header %q", calls, formalHeader)
	}
}

func TestForwardPersistsUpstreamAuthRejection(t *testing.T) {
	c, err := config.Parse([]byte(`{"models":["gpt-6-astra"]}`))
	if err != nil {
		t.Fatal(err)
	}
	cooldown := time.Minute
	var upstreamCalls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls++
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("blocked"))
	}))
	defer server.Close()
	upstreamTransport := server.Client().Transport.(*http.Transport).Clone()
	upstreamTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // local httptest certificate only
	limits := NewLimits(cooldown)
	handler := &Handler{Config: func() *config.Config { return c }, States: turnstate.New(), Limits: limits,
		TransportFactory: func(string, int) (*http.Transport, error) { return upstreamTransport.Clone(), nil }}
	makeStream := func() *fakeStream {
		payload := []byte(`{"model":"gpt-6-astra","input":[]}`)
		return &fakeStream{ctx: context.Background(), requests: []*v1.ForwardRequest{
			{Frame: &v1.ForwardRequest_Start{Start: &v1.ForwardRequestStart{Method: http.MethodPost, Url: server.URL + "/responses", Host: strings.TrimPrefix(server.URL, "https://"), AccountId: 17, Platform: "openai", AccountType: "oauth", ContentLength: int64(len(payload)), HasBody: true}}},
			{Frame: &v1.ForwardRequest_BodyChunk{BodyChunk: payload}},
			{Frame: &v1.ForwardRequest_BodyEnd{BodyEnd: true}},
		}}
	}
	first := makeStream()
	if err := handler.Forward(first); err != nil {
		t.Fatal(err)
	}
	if upstreamCalls != 1 || first.responses[0].GetStart().StatusCode != http.StatusForbidden {
		t.Fatalf("first rejection = calls %d responses %+v", upstreamCalls, first.responses)
	}
	second := makeStream()
	if err := handler.Forward(second); err != nil {
		t.Fatal(err)
	}
	if upstreamCalls != 1 || len(second.responses) != 1 || second.responses[0].GetError().Code != "UPSTREAM_AUTH_REJECTED" {
		t.Fatalf("second rejection = calls %d responses %+v", upstreamCalls, second.responses)
	}
}

func TestForwardPropagatesCancellationAfterRequestSent(t *testing.T) {
	responseReady := make(chan struct{})
	canceledUpstream := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(responseReady)
		<-r.Context().Done()
		close(canceledUpstream)
	}))
	defer server.Close()
	c, err := config.Parse([]byte(`{"models":["gpt-6-astra"]}`))
	if err != nil {
		t.Fatal(err)
	}
	upstreamTransport := server.Client().Transport.(*http.Transport).Clone()
	upstreamTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // local httptest certificate only
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	payload := []byte(`{"model":"gpt-6-astra","input":[]}`)
	stream := &fakeStream{ctx: ctx, requests: []*v1.ForwardRequest{
		{Frame: &v1.ForwardRequest_Start{Start: &v1.ForwardRequestStart{Method: http.MethodPost, Url: server.URL + "/responses", Host: strings.TrimPrefix(server.URL, "https://"), AccountId: 21, Platform: "openai", AccountType: "oauth", ContentLength: int64(len(payload)), HasBody: true}}},
		{Frame: &v1.ForwardRequest_BodyChunk{BodyChunk: payload}},
		{Frame: &v1.ForwardRequest_BodyEnd{BodyEnd: true}},
	}}
	handler := &Handler{Config: func() *config.Config { return c }, TransportFactory: func(string, int) (*http.Transport, error) { return upstreamTransport.Clone(), nil }}
	done := make(chan error, 1)
	go func() { done <- handler.Forward(stream) }()
	select {
	case <-responseReady:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream response was not started")
	}
	cancel()
	select {
	case <-canceledUpstream:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not reach upstream")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(stream.responses) == 0 {
		t.Fatalf("no cancellation response: %+v", stream.responses)
	}
	last := stream.responses[len(stream.responses)-1]
	if lastError := last.GetError(); lastError != nil {
		if (lastError.Code != "UPSTREAM_FAILED" && lastError.Code != "UPSTREAM_READ_FAILED") || !lastError.RequestSent {
			t.Fatalf("unexpected cancellation error response: %+v", stream.responses)
		}
	} else if last.GetEnd() == nil {
		t.Fatalf("cancellation produced neither a terminal error nor end frame: %+v", stream.responses)
	}
}

func TestForwardRejectsChangedStateShapeWithoutReplay(t *testing.T) {
	validState := validTurnState('v')
	var calls int
	var receivedState string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		receivedState = r.Header.Get(turnstate.Header)
		w.Header().Set(turnstate.Header, "wrong-shape")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: response.completed\n\n"))
	}))
	defer server.Close()
	c, err := config.Parse([]byte(fmt.Sprintf(`{"enabled":true,"inject_state":true,"state_target_length":%d,"models":["gpt-6-astra"]}`, len(validState))))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled || !c.InjectState {
		t.Fatalf("unexpected config: %+v", c)
	}
	store := turnstate.New()
	policy, _ := policyForConfig(c)
	if !store.Offer(31, "gpt-6-astra", validState, 0, policy, time.Now()) {
		t.Fatal("failed to seed state")
	}
	if snapshot, ok := store.Acquire(31, "gpt-6-astra", policy, time.Now()); !ok || snapshot.Token.Value == "" {
		t.Fatalf("seeded state is not usable: %+v %v", snapshot, ok)
	}
	upstreamTransport := server.Client().Transport.(*http.Transport).Clone()
	upstreamTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // local httptest certificate only
	payload := []byte(`{"model":"gpt-6-astra","input":[]}`)
	stream := &fakeStream{ctx: context.Background(), requests: []*v1.ForwardRequest{
		{Frame: &v1.ForwardRequest_Start{Start: &v1.ForwardRequestStart{Method: http.MethodPost, Url: server.URL + "/responses", Host: strings.TrimPrefix(server.URL, "https://"), AccountId: 31, Platform: "openai", AccountType: "oauth", ContentLength: int64(len(payload)), HasBody: true}}},
		{Frame: &v1.ForwardRequest_BodyChunk{BodyChunk: payload}},
		{Frame: &v1.ForwardRequest_BodyEnd{BodyEnd: true}},
	}}
	handler := &Handler{Config: func() *config.Config { return c }, States: store,
		TransportFactory: func(string, int) (*http.Transport, error) { return upstreamTransport.Clone(), nil }}
	if err := handler.Forward(stream); err != nil {
		t.Fatal(err)
	}
	if receivedState != validState {
		t.Fatalf("state was not injected: got %q", receivedState)
	}
	if len(stream.responses) != 1 || stream.responses[0].GetError().Code != "STATE_SHAPE_CHANGED" || !stream.responses[0].GetError().RequestSent {
		t.Fatalf("unexpected shape response: %+v", stream.responses)
	}
	if calls != 1 {
		t.Fatalf("formal generation was replayed: calls=%d", calls)
	}
}

func TestForwardKeepsCachedStateWhenResponseHeaderIsMissing(t *testing.T) {
	validState := validTurnState('m')
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: response.completed\n\n"))
	}))
	defer server.Close()
	c, err := config.Parse([]byte(fmt.Sprintf(`{"enabled":true,"inject_state":true,"state_target_length":%d,"models":["gpt-6-astra"]}`, len(validState))))
	if err != nil {
		t.Fatal(err)
	}
	store := turnstate.New()
	policy, _ := policyForConfig(c)
	if !store.Offer(32, "gpt-6-astra", validState, 0, policy, time.Now()) {
		t.Fatal("failed to seed state")
	}
	base := server.Client().Transport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // local httptest certificate only
	payload := []byte(`{"model":"gpt-6-astra","input":[]}`)
	stream := &fakeStream{ctx: context.Background(), requests: []*v1.ForwardRequest{
		{Frame: &v1.ForwardRequest_Start{Start: &v1.ForwardRequestStart{Method: http.MethodPost, Url: server.URL + "/responses", Host: strings.TrimPrefix(server.URL, "https://"), AccountId: 32, Platform: "openai", AccountType: "oauth", ContentLength: int64(len(payload)), HasBody: true}}},
		{Frame: &v1.ForwardRequest_BodyChunk{BodyChunk: payload}},
		{Frame: &v1.ForwardRequest_BodyEnd{BodyEnd: true}},
	}}
	handler := &Handler{Config: func() *config.Config { return c }, States: store,
		TransportFactory: func(string, int) (*http.Transport, error) { return base.Clone(), nil }}
	if err := handler.Forward(stream); err != nil {
		t.Fatal(err)
	}
	if len(stream.responses) != 3 || stream.responses[0].GetStart().StatusCode != http.StatusOK {
		t.Fatalf("missing state header changed response: %+v", stream.responses)
	}
	if got, ok := store.Acquire(32, "gpt-6-astra", policy, time.Now()); !ok || got.Token.Value != validState {
		t.Fatalf("cached state was discarded: %+v %v", got, ok)
	}
}

func TestProbeStreamOutcomeRejectsFailureEvenWithLaterCompletedEvent(t *testing.T) {
	data := []byte("data: {\"type\":\"response.failed\",\"error\":{\"code\":\"rate_limit_exceeded\"}}\n\ndata: {\"type\":\"response.completed\"}\n\n")
	completed, status := probeStreamOutcome(data)
	if completed || status != http.StatusTooManyRequests {
		t.Fatalf("probe outcome = completed %v status %d", completed, status)
	}
}

func TestForwardResponseHeaderTimeoutIsRequestSentFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	c, err := config.Parse([]byte(`{"response_header_timeout_seconds":1,"models":["gpt-6-astra"]}`))
	if err != nil {
		t.Fatal(err)
	}
	base := server.Client().Transport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // local httptest certificate only
	payload := []byte(`{"model":"gpt-6-astra","input":[]}`)
	stream := &fakeStream{ctx: context.Background(), requests: []*v1.ForwardRequest{
		{Frame: &v1.ForwardRequest_Start{Start: &v1.ForwardRequestStart{Method: http.MethodPost, Url: server.URL + "/responses", Host: strings.TrimPrefix(server.URL, "https://"), AccountId: 41, Platform: "openai", AccountType: "oauth", ContentLength: int64(len(payload)), HasBody: true}}},
		{Frame: &v1.ForwardRequest_BodyChunk{BodyChunk: payload}},
		{Frame: &v1.ForwardRequest_BodyEnd{BodyEnd: true}},
	}}
	handler := &Handler{Config: func() *config.Config { return c }, TransportFactory: func(_ string, timeout int) (*http.Transport, error) {
		tr := base.Clone()
		tr.ResponseHeaderTimeout = time.Duration(timeout) * time.Second
		return tr, nil
	}}
	started := time.Now()
	if err := handler.Forward(stream); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) >= 1400*time.Millisecond {
		t.Fatal("response header timeout was not enforced")
	}
	if len(stream.responses) != 1 || stream.responses[0].GetError().Code != "UPSTREAM_FAILED" || !stream.responses[0].GetError().RequestSent {
		t.Fatalf("unexpected timeout response: %+v", stream.responses)
	}
}

func TestForwardDoesNotTreat503AsAccountRejection(t *testing.T) {
	var calls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("unavailable"))
	}))
	defer server.Close()
	c, err := config.Parse([]byte(`{"models":["gpt-6-astra"]}`))
	if err != nil {
		t.Fatal(err)
	}
	base := server.Client().Transport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // local httptest certificate only
	limits := NewLimits(time.Minute)
	handler := &Handler{Config: func() *config.Config { return c }, Limits: limits,
		TransportFactory: func(string, int) (*http.Transport, error) { return base.Clone(), nil }}
	for range 2 {
		payload := []byte(`{"model":"gpt-6-astra","input":[]}`)
		stream := &fakeStream{ctx: context.Background(), requests: []*v1.ForwardRequest{
			{Frame: &v1.ForwardRequest_Start{Start: &v1.ForwardRequestStart{Method: http.MethodPost, Url: server.URL + "/responses", Host: strings.TrimPrefix(server.URL, "https://"), AccountId: 51, Platform: "openai", AccountType: "oauth", ContentLength: int64(len(payload)), HasBody: true}}},
			{Frame: &v1.ForwardRequest_BodyChunk{BodyChunk: payload}},
			{Frame: &v1.ForwardRequest_BodyEnd{BodyEnd: true}},
		}}
		if err := handler.Forward(stream); err != nil {
			t.Fatal(err)
		}
		if stream.responses[0].GetStart().StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("unexpected response: %+v", stream.responses)
		}
	}
	if calls != 2 {
		t.Fatalf("503 incorrectly opened account rejection: calls=%d", calls)
	}
}
