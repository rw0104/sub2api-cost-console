package pluginruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	hclog "github.com/hashicorp/go-hclog"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/hashicorp/go-plugin/runner"
	"github.com/stretchr/testify/require"
)

func TestContainerRejectsRemoteAndUnauthorizedAddresses(t *testing.T) {
	require.False(t, localDockerEndpoint("tcp://remote.example:2375"))
	require.False(t, localDockerEndpoint("ssh://remote.example"))
	require.True(t, localDockerEndpoint("unix:///var/run/docker.sock"))
	require.True(t, localDockerEndpoint("npipe:////./pipe/docker_engine"))
	c := &Container{}
	for _, address := range []string{"/etc/passwd", "/rpc/../var/run/docker.sock", "/rpc/socket/.."} {
		_, _, err := c.PluginToHost("unix", address)
		require.Error(t, err)
	}
	_, _, err := c.PluginToHost("tcp", "127.0.0.1:5432")
	require.Error(t, err)
}

func TestContainerRequiresDockerWithoutProcessFallback(t *testing.T) {
	t.Setenv("PATH", "")
	container, err := NewContainer(ContainerOptions{MemoryMB: 256, CPUMilli: 1000, PidsLimit: 64})
	require.Nil(t, container)
	require.ErrorContains(t, err, "requires a local Docker Engine")
}

func TestContainerArgumentsKeepIsolationAndFilterEnvironment(t *testing.T) {
	c := &Container{name: "test", options: ContainerOptions{WorkDir: t.TempDir(), MemoryMB: 256, CPUMilli: 1000, PidsLimit: 64,
		Env: []string{"SUB2API_PLUGIN_MAGIC_COOKIE=expected", "PLUGIN_CLIENT_CERT=public-cert", "DATABASE_PASSWORD=secret", "PLUGIN_UNIX_SOCKET_DIR=/host"}}}
	args, err := c.createArgs("sha256:test", filepath.Join(c.options.WorkDir, "lease"))
	require.NoError(t, err)
	command := strings.Join(args, " ")
	for _, value := range []string{"--pull=never", "--network none", "--read-only", "--cap-drop ALL", "--security-opt no-new-privileges=true",
		"--memory 256m", "--memory-swap 256m", "--pids-limit 64", "--cpus 1.000", "PLUGIN_UNIX_SOCKET_DIR=/rpc"} {
		require.Contains(t, command, value)
	}
	require.NotContains(t, command, "DATABASE_PASSWORD")
	require.NotContains(t, command, "/var/run/docker.sock")
	require.NotContains(t, command, "PLUGIN_UNIX_SOCKET_DIR=/host")
	require.NotContains(t, command, "--privileged")
}

func TestContainerArgumentsExposeOnlyConfiguredEgressSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "egress.sock")
	c := &Container{name: "test", options: ContainerOptions{WorkDir: t.TempDir(), MemoryMB: 256, CPUMilli: 1000, PidsLimit: 64,
		EgressBroker: EgressBrokerOptions{Enabled: true, SocketPath: socketPath,
			AllowedHosts: []string{"api.openai.com"}, AllowedSchemes: []string{"https"}, RequireTLS: true}}}
	args, err := c.createArgs("sha256:test", filepath.Join(c.options.WorkDir, "lease"))
	require.NoError(t, err)
	command := strings.Join(args, " ")
	require.Contains(t, command, "--network none")
	require.Contains(t, command, "--mount type=bind,src=")
	require.Contains(t, command, EgressBrokerContainerSocket)
	require.Contains(t, command, "SUB2API_PLUGIN_EGRESS_BROKER_SOCKET="+EgressBrokerContainerSocket)
	require.NotContains(t, command, "--network host")
	require.NotContains(t, command, "--env HTTP_PROXY=")
	require.NotContains(t, command, "--env HTTPS_PROXY=")
}

func TestEgressBrokerOptionsFailClosed(t *testing.T) {
	for _, options := range []EgressBrokerOptions{
		{Enabled: true, SocketPath: "relative.sock", AllowedHosts: []string{"api.openai.com"}, AllowedSchemes: []string{"https"}, RequireTLS: true},
		{Enabled: true, SocketPath: "/run/egress.sock", AllowedHosts: []string{"api.openai.com"}, AllowedSchemes: []string{"http"}, RequireTLS: false},
		{Enabled: true, SocketPath: "/run/egress.sock", AllowedHosts: []string{"*"}, AllowedSchemes: []string{"https"}, RequireTLS: true},
	} {
		require.Error(t, options.Validate())
	}
}

func TestContainerIsolationIntegration(t *testing.T) {
	if os.Getenv("SUB2API_TEST_SANDBOX_CONTAINER") != "1" {
		t.Skip("set SUB2API_TEST_SANDBOX_CONTAINER=1 after building the sandbox image")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	binary := filepath.Join(root, "probe")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./internal/pluginruntime/testdata/probe")
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	hash := sha256.Sum256(data)
	secretPath := filepath.Join(root, "host-private")
	require.NoError(t, os.WriteFile(secretPath, []byte("host-only marker"), 0600))
	t.Setenv("SUB2API_SANDBOX_TEST_SECRET", "host-only marker")
	var container *Container
	newClient := func() *hcplugin.Client {
		return hcplugin.NewClient(&hcplugin.ClientConfig{
			HandshakeConfig: pluginv2.HandshakeConfig, Plugins: pluginv2.ClientPluginMap(), AllowedProtocols: []hcplugin.Protocol{hcplugin.ProtocolGRPC},
			AutoMTLS: true, SkipHostEnv: true, StartTimeout: 30 * time.Second, UnixSocketConfig: &hcplugin.UnixSocketConfig{TempDir: root},
			Logger: hclog.NewNullLogger(),
			RunnerFunc: func(_ hclog.Logger, spec *exec.Cmd, workDir string) (runner.Runner, error) {
				var err error
				container, err = NewContainer(ContainerOptions{BinaryPath: binary, BinarySHA256: hex.EncodeToString(hash[:]), WorkDir: workDir,
					Image: "sub2api-plugin-sandbox:1", MemoryMB: 64, CPUMilli: 1000, PidsLimit: 64, Env: spec.Env})
				return container, err
			},
		})
	}
	client := newClient()
	defer client.Kill()
	rpc, err := client.Client()
	require.NoError(t, err)
	dispensed, err := rpc.Dispense(pluginv2.ExtensionPluginName)
	require.NoError(t, err)
	api, ok := dispensed.(pluginv2.ExtensionHandler)
	require.True(t, ok, "dispensed plugin must implement the extension API")
	info, err := api.GetInfo(ctx)
	require.NoError(t, err)
	require.Equal(t, "example.sandbox-probe", info.PluginID)
	cfg, _ := json.Marshal(map[string]string{"host_path": secretPath})
	require.NoError(t, api.ApplyConfig(ctx, cfg))
	request := pluginv2.PreprocessRequest{Capability: pluginv2.CapabilityRequestPreprocess, Context: pluginv2.RequestContext{
		RequestID: "sandbox-test", Deadline: time.Now().Add(10 * time.Second), Platform: "openai", AccountType: "oauth", Method: "POST", Path: "/v1/responses"}}
	response, err := api.Preprocess(ctx, request)
	require.NoError(t, err)
	var observed map[string]any
	require.NoError(t, json.Unmarshal([]byte(response.Reason), &observed))
	require.EqualValues(t, 65532, observed["uid"])
	require.EqualValues(t, 65532, observed["gid"])
	for _, field := range []string{"host_readable", "root_writable", "binary_writable", "network_accessible", "host_env_exposed"} {
		require.Equal(t, false, observed[field], field)
	}
	require.Equal(t, true, observed["temp_writable"])
	require.Equal(t, "0000000000000000", observed["cap_effective"])
	require.Empty(t, observed["supplementary_groups"])
	require.Equal(t, []any{"lo"}, observed["interfaces"])
	require.Equal(t, "67108864", observed["memory_max"])
	require.Equal(t, "64", observed["pids_max"])
	require.Equal(t, "100000 100000", observed["cpu_max"])
	t.Logf("Verified kernel isolation: %s", response.Reason)
	request.Context.Model = "allocate"
	request.Context.Deadline = time.Now().Add(15 * time.Second)
	eventStart := time.Now().Add(-time.Second).Unix()
	_, err = api.Preprocess(ctx, request)
	require.Error(t, err, "exceeding cgroup memory must terminate the plugin")
	require.Eventually(t, client.Exited, 20*time.Second, 100*time.Millisecond)
	eventCtx, eventCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer eventCancel()
	events, err := exec.CommandContext(eventCtx, "docker", "events", "--since", strconv.FormatInt(eventStart, 10),
		"--until", strconv.FormatInt(time.Now().Unix()+1, 10), "--filter", "container="+container.ID(), "--filter", "event=oom", "--format", "{{.Action}}").CombinedOutput()
	require.NoError(t, err, string(events))
	require.Contains(t, string(events), "oom", "the daemon must confirm a cgroup OOM, not merely an RPC timeout")
	hostBytes, err := os.ReadFile(secretPath)
	require.NoError(t, err)
	require.Equal(t, "host-only marker", string(hostBytes))
	require.NotEmpty(t, container.ID())

	t.Run("host lease loss", func(t *testing.T) {
		orphan := newClient()
		defer orphan.Kill()
		_, err := orphan.Client()
		require.NoError(t, err)
		started := time.Now()
		// Stop only the host's lease writes. Keep its control connection and
		// Docker attachment alive so the in-container watchdog must do the work.
		require.NoError(t, container.lease.Close())
		require.Eventually(t, orphan.Exited, 25*time.Second, 100*time.Millisecond)
		require.GreaterOrEqual(t, time.Since(started), 14*time.Second)
	})
}
