// verify-plugin-devkit validates the generated public example through its real
// subprocess handshake and SDK RPC. Run from the extracted backend Go module.
package main

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"time"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
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
	binary := flag.String("binary", "", "Built public example")
	archive := flag.String("package", "", "Signed public example package")
	publicPath := flag.String("public-key", "", "Verification public key")
	flag.Parse()
	reader, err := zip.OpenReader(*archive)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	files := map[string][]byte{}
	for _, file := range reader.File {
		entry, err := file.Open()
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(entry)
		_ = entry.Close()
		if readErr != nil {
			return readErr
		}
		files[file.Name] = data
	}
	var manifest struct {
		ID           string                `json:"id"`
		Version      string                `json:"version"`
		Capabilities []pluginv2.Capability `json:"capabilities"`
		Files        map[string]string     `json:"files"`
		Runtimes     map[string]struct {
			Path string `json:"path"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		return err
	}
	var signature struct {
		Signature string `json:"signature"`
		Algorithm string `json:"algorithm"`
		KeyID     string `json:"key_id"`
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(files["signature.json"], &signature); err != nil {
		return err
	}
	keyText, err := os.ReadFile(*publicPath)
	if err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyText)))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return errors.New("invalid public key")
	}
	if signature.PublicKey != base64.StdEncoding.EncodeToString(key) {
		return errors.New("shareable package must include the publisher public key")
	}
	sig, err := base64.StdEncoding.DecodeString(signature.Signature)
	if err != nil || signature.Algorithm != "ed25519" || signature.KeyID != "devkit-verification" || !ed25519.Verify(key, files["manifest.json"], sig) {
		return errors.New("invalid package signature")
	}
	for name, hash := range manifest.Files {
		sum := sha256.Sum256(files[name])
		if hex.EncodeToString(sum[:]) != hash {
			return fmt.Errorf("file hash mismatch: %s", name)
		}
	}
	data, err := os.ReadFile(*binary)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	runtimePath := manifest.Runtimes[runtime.GOOS+"-"+runtime.GOARCH].Path
	if manifest.Files[runtimePath] != hex.EncodeToString(sum[:]) {
		return errors.New("runtime differs from signed package")
	}
	client := hcplugin.NewClient(&hcplugin.ClientConfig{HandshakeConfig: pluginv2.HandshakeConfig, Plugins: pluginv2.ClientPluginMap(), Cmd: exec.Command(*binary), AllowedProtocols: []hcplugin.Protocol{hcplugin.ProtocolGRPC}, Logger: hclog.NewNullLogger(), StartTimeout: 10 * time.Second, AutoMTLS: true, SkipHostEnv: true})
	defer client.Kill()
	rpc, err := client.Client()
	if err != nil {
		return err
	}
	raw, err := rpc.Dispense(pluginv2.ExtensionPluginName)
	if err != nil {
		return err
	}
	handler, ok := raw.(pluginv2.ExtensionHandler)
	if !ok {
		return errors.New("extension API missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := handler.GetInfo(ctx)
	if err != nil {
		return err
	}
	if info.PluginID != manifest.ID || info.PluginVersion != manifest.Version || !reflect.DeepEqual(info.Capabilities, manifest.Capabilities) {
		return errors.New("runtime and manifest identity differ")
	}
	health, err := handler.Health(ctx)
	if err != nil || !health.Healthy {
		return errors.New("unhealthy example process")
	}
	config, err := handler.ValidateConfig(ctx, []byte(`{"max_output_tokens":12}`))
	if err != nil {
		return err
	}
	if err = handler.ApplyConfig(ctx, config); err != nil {
		return err
	}
	if _, err = handler.TestConfig(ctx, config); err != nil {
		return err
	}
	request := pluginv2.PreprocessRequest{Capability: pluginv2.CapabilityRequestPreprocess, Context: pluginv2.RequestContext{RequestID: "devkit-validation", Deadline: time.Now().Add(2 * time.Second), Platform: "openai", AccountType: "oauth", AccountID: 7, Method: "POST", Path: "/v1/responses", Model: "synthetic"}, BodyJSON: []byte(`{"model":"synthetic","input":"synthetic"}`)}
	response, err := handler.Preprocess(ctx, request)
	if err != nil {
		return err
	}
	if response.Decision != pluginv2.DecisionModify || response.Patch == nil || !response.Patch.BodyChanged {
		return errors.New("example did not modify the request")
	}
	var body map[string]any
	if err = json.Unmarshal(response.Patch.BodyJSON, &body); err != nil {
		return err
	}
	if body["max_output_tokens"] != float64(12) || body["input"] != "synthetic" || body["model"] != "synthetic" {
		return errors.New("unexpected example patch")
	}
	fmt.Println("Verified Ed25519 package, runtime identity, mTLS handshake, health, configuration and request preprocessing")
	return nil
}
