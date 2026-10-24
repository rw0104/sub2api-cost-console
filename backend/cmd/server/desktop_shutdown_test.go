package main

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestDesktopShutdownOnlyAcceptsExplicitSupervisorCommand(t *testing.T) {
	t.Setenv("SUB2API_DESKTOP", "1")
	t.Setenv("SUB2API_DESKTOP_CONTROL", "stdin-v1")
	requests := desktopShutdownRequests(strings.NewReader("unrelated input\nsub2api:desktop:shutdown:v1\n"))
	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("managed shutdown command was ignored")
	}
	for _, input := range []string{"", "shutdown\n", "sub2api:desktop:shutdown:v2\n"} {
		select {
		case <-desktopShutdownRequests(strings.NewReader(input)):
			t.Fatalf("EOF or invalid input must not stop the server: %q", input)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestDesktopShutdownNeverConsumesServerStdin(t *testing.T) {
	t.Setenv("SUB2API_DESKTOP", "")
	t.Setenv("SUB2API_DESKTOP_CONTROL", "stdin-v1")
	if got := desktopShutdownRequests(forbiddenStdin{t}); got != nil {
		t.Fatal("server mode must not register a desktop shutdown channel")
	}
	t.Setenv("SUB2API_DESKTOP", "1")
	t.Setenv("SUB2API_DESKTOP_CONTROL", "")
	if got := desktopShutdownRequests(forbiddenStdin{t}); got != nil {
		t.Fatal("old desktop supervisors must not acquire a control pipe")
	}
}

type forbiddenStdin struct{ t *testing.T }

func (r forbiddenStdin) Read([]byte) (int, error) {
	r.t.Error("unexpected stdin read")
	return 0, io.EOF
}
