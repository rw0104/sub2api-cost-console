package config

import (
	"errors"
	"net"
	"path"
	"regexp"
	"strings"
)

// PluginSandboxEgressBrokerConfig describes the host-owned egress boundary
// reserved for network-restricted containers. The broker is deliberately
// configured separately from the container image and never enables Docker
// networking on its own.
type PluginSandboxEgressBrokerConfig struct {
	Enabled        bool     `mapstructure:"enabled"`
	SocketPath     string   `mapstructure:"socket_path"`
	AllowedHosts   []string `mapstructure:"allowed_hosts"`
	AllowedSchemes []string `mapstructure:"allowed_schemes"`
	RequireTLS     bool     `mapstructure:"require_tls"`
}

var sandboxEgressHostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)

func (c PluginSandboxEgressBrokerConfig) WithDefaults() PluginSandboxEgressBrokerConfig {
	if !c.Enabled {
		return c
	}
	if len(c.AllowedSchemes) == 0 {
		c.AllowedSchemes = []string{"https"}
	}
	return c
}

func (c PluginSandboxEgressBrokerConfig) Validate() error {
	c = c.WithDefaults()
	if !c.Enabled {
		if strings.TrimSpace(c.SocketPath) != "" || len(c.AllowedHosts) > 0 || len(c.AllowedSchemes) > 0 {
			return errors.New("plugins.v2_sandbox.egress_broker is disabled but contains policy")
		}
		return nil
	}
	if strings.TrimSpace(c.SocketPath) == "" || !path.IsAbs(c.SocketPath) {
		return errors.New("plugins.v2_sandbox.egress_broker.socket_path must be an absolute host path")
	}
	if path.Clean(c.SocketPath) != c.SocketPath || strings.ContainsAny(c.SocketPath, ",\r\n\x00") {
		return errors.New("plugins.v2_sandbox.egress_broker.socket_path is invalid")
	}
	if len(c.AllowedHosts) == 0 || len(c.AllowedHosts) > 64 {
		return errors.New("plugins.v2_sandbox.egress_broker.allowed_hosts must contain 1 to 64 hosts")
	}
	hosts := make(map[string]struct{}, len(c.AllowedHosts))
	for _, raw := range c.AllowedHosts {
		host := strings.ToLower(strings.TrimSpace(raw))
		if host == "" || host != raw || strings.ContainsAny(host, "/:*?[]\r\n\x00") || net.ParseIP(host) != nil || !sandboxEgressHostPattern.MatchString(host) {
			return errors.New("plugins.v2_sandbox.egress_broker.allowed_hosts contains an invalid host")
		}
		if _, exists := hosts[host]; exists {
			return errors.New("plugins.v2_sandbox.egress_broker.allowed_hosts contains duplicates")
		}
		hosts[host] = struct{}{}
	}
	if !c.RequireTLS {
		return errors.New("plugins.v2_sandbox.egress_broker.require_tls must be true")
	}
	schemes := make(map[string]struct{}, len(c.AllowedSchemes))
	for _, raw := range c.AllowedSchemes {
		scheme := strings.ToLower(strings.TrimSpace(raw))
		if scheme != raw || (scheme != "https" && scheme != "wss") {
			return errors.New("plugins.v2_sandbox.egress_broker.allowed_schemes only permits https or wss")
		}
		if _, exists := schemes[scheme]; exists {
			return errors.New("plugins.v2_sandbox.egress_broker.allowed_schemes contains duplicates")
		}
		schemes[scheme] = struct{}{}
	}
	return nil
}

func (c PluginSandboxConfig) WithDefaults() PluginSandboxConfig {
	if c.Mode == "" {
		c.Mode = "process"
	}
	if c.Image == "" {
		c.Image = "sub2api-plugin-sandbox:1"
	}
	if c.MemoryMB == 0 {
		c.MemoryMB = 256
	}
	if c.CPUMilli == 0 {
		c.CPUMilli = 1000
	}
	if c.PidsLimit == 0 {
		c.PidsLimit = 64
	}
	c.EgressBroker = c.EgressBroker.WithDefaults()
	return c
}
func (c PluginSandboxConfig) Validate() error {
	c = c.WithDefaults()
	if c.Mode != "process" && c.Mode != "container" {
		return errors.New("plugins.v2_sandbox.mode must be process or container")
	}
	if strings.TrimSpace(c.Image) != c.Image || strings.HasPrefix(c.Image, "-") || strings.ContainsAny(c.Image, "\x00\r\n ") {
		return errors.New("plugins.v2_sandbox.image is invalid")
	}
	if c.MemoryMB < 64 || c.MemoryMB > 4096 {
		return errors.New("plugins.v2_sandbox.memory_mb must be between 64 and 4096")
	}
	if c.CPUMilli < 100 || c.CPUMilli > 4000 {
		return errors.New("plugins.v2_sandbox.cpu_milli must be between 100 and 4000")
	}
	if c.PidsLimit < 32 || c.PidsLimit > 256 {
		return errors.New("plugins.v2_sandbox.pids_limit must be between 32 and 256")
	}
	if c.Mode != "container" && c.EgressBroker.Enabled {
		return errors.New("plugins.v2_sandbox.egress_broker is only available in container mode")
	}
	if err := c.EgressBroker.Validate(); err != nil {
		return err
	}
	return nil
}
