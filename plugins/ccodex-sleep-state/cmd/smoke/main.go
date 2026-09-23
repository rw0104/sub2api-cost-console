package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	v2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/hashicorp/go-hclog"
	hcplugin "github.com/hashicorp/go-plugin"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	binary := flag.String("plugin", "", "plugin runtime executable")
	flag.Parse()
	if *binary == "" {
		return errors.New("-plugin is required")
	}
	path, err := filepath.Abs(*binary)
	if err != nil {
		return err
	}
	command := exec.Command(path)
	client := hcplugin.NewClient(&hcplugin.ClientConfig{
		HandshakeConfig:  v2.HandshakeConfig,
		Plugins:          v2.ClientPluginMap(),
		Cmd:              command,
		AllowedProtocols: []hcplugin.Protocol{hcplugin.ProtocolGRPC},
		StartTimeout:     10 * time.Second,
		Logger:           hclog.NewNullLogger(),
		SkipHostEnv:      true,
	})
	defer client.Kill()
	rpc, err := client.Client()
	if err != nil {
		return err
	}
	raw, err := rpc.Dispense(v2.ExtensionPluginName)
	if err != nil {
		return err
	}
	handler := raw.(v2.ExtensionHandler)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := handler.GetInfo(ctx)
	if err != nil {
		return err
	}
	if err := info.Validate(); err != nil {
		return err
	}
	health, err := handler.Health(ctx)
	if err != nil || !health.Healthy {
		return fmt.Errorf("unhealthy runtime: %v", err)
	}
	normalized, err := handler.ValidateConfig(ctx, []byte(`{"enabled":false}`))
	if err != nil {
		return err
	}
	if err := handler.ApplyConfig(ctx, normalized); err != nil {
		return err
	}
	latency, err := handler.TestConfig(ctx, normalized)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"passed":  true,
		"plugin":  info.PluginID,
		"version": info.PluginVersion,
		"healthy": health.Healthy,
		"test_ms": latency.Milliseconds(),
	})
}
