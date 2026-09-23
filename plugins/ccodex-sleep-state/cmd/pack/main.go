package main

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var targetPattern = regexp.MustCompile(`^(windows|linux)-(amd64|arm64)$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	output := flag.String("out", ".build/release/ccodex-sleep-state-0.5.1.s2plugin", "new .s2plugin output")
	keyPath := flag.String("key", "", "Base64 Ed25519 private key outside this plugin directory")
	keyID := flag.String("key-id", "", "publisher key ID")
	generateKey := flag.Bool("generate-key", false, "create the key when it does not exist")
	targetsText := flag.String("targets", "windows-amd64,linux-amd64,linux-arm64", "comma-separated runtime targets")
	flag.Parse()
	if *keyPath == "" || *keyID == "" {
		return errors.New("-key and -key-id are required")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`).MatchString(*keyID) {
		return errors.New("invalid publisher key ID")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	keyFile, err := filepath.Abs(*keyPath)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, keyFile)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("signing key must be outside the plugin directory")
	}
	if *generateKey {
		if _, statErr := os.Stat(keyFile); os.IsNotExist(statErr) {
			if err := writePrivateKey(keyFile); err != nil {
				return err
			}
		}
	}
	private, err := readPrivateKey(keyFile)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(*output); !os.IsNotExist(statErr) {
		return errors.New("output already exists or is inaccessible")
	}
	targets, err := parseTargets(*targetsText)
	if err != nil {
		return err
	}

	files := map[string][]byte{}
	for _, name := range []string{"README.md", "CHANGELOG.md", "LICENSE", "THIRD_PARTY_NOTICES.md", "docs/INSTALL.md", "docs/VERIFICATION.md", "docs/CORE.md", "provenance/source-manifest.json", "ui/index.html", "ui/app.js", "ui/style.css"} {
		data, readErr := os.ReadFile(filepath.FromSlash(name))
		if readErr != nil {
			return fmt.Errorf("read asset %s: %w", name, readErr)
		}
		files[name] = data
	}
	upstreamLicense, err := os.ReadFile(filepath.Join("internal", "upstream", "LICENSE"))
	if err != nil {
		return fmt.Errorf("read upstream core license: %w", err)
	}
	files["licenses/CCodex-Sleep-State-LICENSE"] = upstreamLicense
	projectLicense, err := os.ReadFile(filepath.Join("..", "..", "LICENSE"))
	if err != nil {
		return fmt.Errorf("read Sub2API license: %w", err)
	}
	files["licenses/Sub2API-LICENSE"] = projectLicense
	goRoot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return err
	}
	goLicense, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(goRoot)), "LICENSE"))
	if err != nil {
		return fmt.Errorf("read Go license: %w", err)
	}
	files["licenses/Go-LICENSE"] = goLicense
	mihomoDir, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/metacubex/mihomo").Output()
	if err != nil {
		return fmt.Errorf("locate Mihomo module: %w", err)
	}
	mihomoLicense, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(mihomoDir)), "LICENSE"))
	if err != nil {
		return fmt.Errorf("read Mihomo license: %w", err)
	}
	files["licenses/Mihomo-LICENSE"] = mihomoLicense
	manifestBytes, err := os.ReadFile("manifest.source.json")
	if err != nil {
		return err
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return err
	}
	runtimes := map[string]map[string]string{}
	for _, target := range targets {
		parts := strings.SplitN(target, "-", 2)
		runtimePath := "runtimes/" + target + "/plugin"
		if parts[0] == "windows" {
			runtimePath += ".exe"
		}
		binaryPath := filepath.Join(".build", filepath.FromSlash(runtimePath))
		if err := os.MkdirAll(filepath.Dir(binaryPath), 0700); err != nil {
			return err
		}
		cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", binaryPath, "./cmd/plugin")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS="+parts[0], "GOARCH="+parts[1], "CGO_ENABLED=0")
		if output, buildErr := cmd.CombinedOutput(); buildErr != nil {
			return fmt.Errorf("build %s: %w\n%s", target, buildErr, output)
		}
		data, readErr := os.ReadFile(binaryPath)
		if readErr != nil {
			return readErr
		}
		files[runtimePath] = data
		runtimes[target] = map[string]string{"path": runtimePath}
	}
	manifest["runtimes"] = runtimes
	hashes := make(map[string]string, len(files))
	for name, data := range files {
		sum := sha256.Sum256(data)
		hashes[name] = hex.EncodeToString(sum[:])
	}
	manifest["files"] = hashes
	manifestBytes, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	files["manifest.json"] = manifestBytes
	public := private.Public().(ed25519.PublicKey)
	files["signature.json"], err = json.MarshalIndent(map[string]string{
		"algorithm":  "ed25519",
		"key_id":     *keyID,
		"public_key": base64.StdEncoding.EncodeToString(public),
		"signature":  base64.StdEncoding.EncodeToString(ed25519.Sign(private, manifestBytes)),
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0700); err != nil {
		return err
	}
	if err := writeZip(*output, files); err != nil {
		return err
	}
	packageBytes, err := os.ReadFile(*output)
	if err != nil {
		return err
	}
	packageSum := sha256.Sum256(packageBytes)
	if err := os.WriteFile(*output+".sha256", []byte(hex.EncodeToString(packageSum[:])+"  "+filepath.Base(*output)+"\n"), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(*output), "publisher-public-key.txt"), []byte(base64.StdEncoding.EncodeToString(public)+"\n"), 0600); err != nil {
		return err
	}
	trusted := "plugins:\n  v2_sandbox:\n    mode: process\n  trusted_publishers:\n    " + *keyID + ": '" + base64.StdEncoding.EncodeToString(public) + "'\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(*output), "trusted-publisher.yaml"), []byte(trusted), 0600); err != nil {
		return err
	}
	fmt.Println("Created", *output)
	return nil
}

func parseTargets(raw string) ([]string, error) {
	seen := map[string]struct{}{}
	var targets []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if !targetPattern.MatchString(item) {
			return nil, fmt.Errorf("unsupported target %q", item)
		}
		if _, exists := seen[item]; exists {
			return nil, fmt.Errorf("duplicate target %q", item)
		}
		seen[item] = struct{}{}
		targets = append(targets, item)
	}
	if len(targets) == 0 {
		return nil, errors.New("at least one runtime target is required")
	}
	return targets, nil
}

func writePrivateKey(path string) error {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(private)), 0600)
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Base64 Ed25519 private key")
	}
	private := ed25519.PrivateKey(key)
	if !bytes.Equal(ed25519.NewKeyFromSeed(private.Seed()), private) {
		return nil, errors.New("inconsistent Ed25519 private key")
	}
	return private, nil
}

func writeZip(path string, files map[string][]byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	archive := zip.NewWriter(file)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0600)
		if strings.HasPrefix(name, "runtimes/") {
			header.SetMode(0700)
		}
		entry, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		if _, err := entry.Write(files[name]); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}
