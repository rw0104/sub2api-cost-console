package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"time"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
)

type probe struct{ hostPath string }

func main() { pluginv2.Serve(&probe{}) }
func (*probe) GetInfo(context.Context) (pluginv2.PluginInfo, error) {
	return pluginv2.PluginInfo{PluginID: "example.sandbox-probe", PluginVersion: "0.1.0", ProtocolVersion: 2,
		Capabilities: []pluginv2.Capability{{ID: pluginv2.CapabilityRequestPreprocess, Kind: pluginv2.CapabilityKindHook,
			Platform: "openai", AccountType: "oauth", Permissions: []pluginv2.Permission{pluginv2.PermissionRequestMetadata},
			TimeoutMS: 2000, FailureMode: pluginv2.FailureModeClosed, Synchronous: true}}}, nil
}
func (*probe) Health(context.Context) (pluginv2.HealthStatus, error) {
	return pluginv2.HealthStatus{Healthy: true}, nil
}
func (*probe) ValidateConfig(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return raw, nil
}
func (p *probe) ApplyConfig(_ context.Context, raw json.RawMessage) error {
	var cfg struct {
		HostPath string `json:"host_path"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	p.hostPath = cfg.HostPath
	return nil
}
func (*probe) TestConfig(context.Context, json.RawMessage) (time.Duration, error) { return 0, nil }
func (p *probe) Preprocess(_ context.Context, r pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
	if r.Context.Model == "allocate" {
		// Deliberately exceed the test's 64 MiB cgroup limit; the host must
		// survive and observe this sandbox process terminate.
		var blocks [][]byte
		for i := 0; i < 64; i++ {
			block := make([]byte, 4<<20)
			for j := 0; j < len(block); j += 4096 {
				block[j] = byte(i + 1)
			}
			blocks = append(blocks, block)
		}
		os.Stdout.Write([]byte{blocks[0][0]})
	}
	_, hostErr := os.ReadFile(p.hostPath)
	rootErr := os.WriteFile("/sandbox-write-denied", []byte("test"), 0600)
	binary, writeErr := os.OpenFile("/plugin/runtime", os.O_WRONLY, 0)
	if binary != nil {
		_ = binary.Close()
	}
	tempErr := os.WriteFile("/tmp/probe", []byte("test"), 0600)
	conn, networkErr := net.DialTimeout("tcp", "198.51.100.1:443", 300*time.Millisecond)
	if conn != nil {
		_ = conn.Close()
	}
	status, _ := os.ReadFile("/proc/self/status")
	caps := ""
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "CapEff:") {
			caps = strings.TrimSpace(strings.TrimPrefix(line, "CapEff:"))
		}
	}
	memory, _ := os.ReadFile("/sys/fs/cgroup/memory.max")
	pids, _ := os.ReadFile("/sys/fs/cgroup/pids.max")
	cpu, _ := os.ReadFile("/sys/fs/cgroup/cpu.max")
	groups, _ := os.Getgroups()
	interfaces, _ := net.Interfaces()
	interfaceNames := make([]string, 0, len(interfaces))
	for _, iface := range interfaces {
		interfaceNames = append(interfaceNames, iface.Name)
	}
	observations := map[string]any{"uid": os.Getuid(), "gid": os.Getgid(), "host_readable": hostErr == nil,
		"root_writable": rootErr == nil, "binary_writable": writeErr == nil, "temp_writable": tempErr == nil,
		"network_accessible": networkErr == nil, "host_env_exposed": os.Getenv("SUB2API_SANDBOX_TEST_SECRET") != "",
		"cap_effective": caps, "supplementary_groups": groups, "interfaces": interfaceNames, "memory_max": strings.TrimSpace(string(memory)), "pids_max": strings.TrimSpace(string(pids)), "cpu_max": strings.TrimSpace(string(cpu))}
	data, _ := json.Marshal(observations)
	return pluginv2.PreprocessResponse{Decision: pluginv2.DecisionPass, Reason: string(data)}, nil
}
