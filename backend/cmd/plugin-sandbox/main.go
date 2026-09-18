//go:build linux

// plugin-sandbox is the trusted PID 1 supervisor and Unix-socket bridge inside
// the optional v2 sandbox image. It is not part of an uploaded plugin package.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) == 3 && os.Args[1] == "bridge" {
		return bridge(os.Args[2])
	}
	if len(os.Args) == 2 && os.Args[1] == "supervise" {
		return supervise()
	}
	return errors.New("expected supervise or bridge command")
}
func bridge(socket string) error {
	if os.Getuid() == 0 {
		return errors.New("bridge must run as the restricted user")
	}
	if path.Clean(socket) != socket || !strings.HasPrefix(socket, "/rpc/") || len(socket) > 108 {
		return errors.New("invalid RPC socket")
	}
	// Only a canonical container-local /rpc/ Unix socket is accepted above;
	// this cannot select a network host or scheme.
	conn, err := net.DialTimeout("unix", socket, 5*time.Second) // #nosec G704 -- restricted Unix-domain IPC, not an outbound network request.
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		if unix, ok := conn.(*net.UnixConn); ok {
			_ = unix.CloseWrite()
		}
	}()
	_, err = io.Copy(os.Stdout, conn)
	return err
}
func supervise() error {
	if os.Getuid() != 0 {
		return errors.New("supervisor requires its container-local root identity")
	}
	lease, err := os.ReadFile("/lease")
	if err != nil {
		return fmt.Errorf("read host lease: %w", err)
	}
	cmd := exec.Command("/plugin/runtime")
	allowedEnv := map[string]bool{"SUB2API_PLUGIN_MAGIC_COOKIE": true, "PLUGIN_PROTOCOL_VERSIONS": true, "PLUGIN_CLIENT_CERT": true,
		"PLUGIN_MULTIPLEX_GRPC": true, "PLUGIN_MIN_PORT": true, "PLUGIN_MAX_PORT": true, "PLUGIN_UNIX_SOCKET_DIR": true,
		"HOME": true, "TMPDIR": true, "GOMAXPROCS": true}
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok && allowedEnv[key] {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Dir = "/tmp"
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65532, Gid: 65532}, Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastChange := time.Now()
	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			current, err := os.ReadFile("/lease")
			if err == nil && string(current) != string(lease) {
				lease = current
				lastChange = time.Now()
			}
			if time.Since(lastChange) > 15*time.Second {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				select {
				case <-done:
				case <-time.After(time.Second):
				}
				// Exiting PID 1 also terminates any child that changed its group.
				return context.DeadlineExceeded
			}
		}
	}
}
