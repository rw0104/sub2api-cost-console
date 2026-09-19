package main

import (
	"bufio"
	"io"
	"os"

	"github.com/Wei-Shaw/sub2api/internal/pkg/sysutil"
)

// Only the supervising desktop owns this inherited pipe. No HTTP endpoint or
// credential is added. EOF is not a shutdown request (legacy shells may close
// stdin), and forced process-tree termination remains the bounded fallback.
func desktopShutdownRequests(input io.Reader) <-chan struct{} {
	if !sysutil.IsDesktopMode() || os.Getenv("SUB2API_DESKTOP_CONTROL") != "stdin-v1" {
		return nil
	}
	requested := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 128), 1024)
		for scanner.Scan() {
			if scanner.Text() == "sub2api:desktop:shutdown:v1" {
				close(requested)
				return
			}
		}
	}()
	return requested
}
