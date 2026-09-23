package main

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"debug/elf"
	"debug/pe"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	packagePath := flag.String("package", "", ".s2plugin path")
	publicKeyPath := flag.String("public-key", "", "Base64 publisher public key")
	keyID := flag.String("key-id", "", "expected publisher key ID")
	flag.Parse()
	if *packagePath == "" || *publicKeyPath == "" || *keyID == "" {
		return errors.New("-package, -public-key and -key-id are required")
	}
	publicRaw, err := os.ReadFile(*publicKeyPath)
	if err != nil {
		return err
	}
	public, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(publicRaw)))
	if err != nil || len(public) != ed25519.PublicKeySize {
		return errors.New("invalid Base64 Ed25519 public key")
	}
	archive, err := zip.OpenReader(*packagePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	files := map[string][]byte{}
	for _, entry := range archive.File {
		if entry.Name == "" || strings.HasPrefix(entry.Name, "/") || strings.Contains(entry.Name, "..") {
			return fmt.Errorf("unsafe package path %q", entry.Name)
		}
		if _, duplicate := files[entry.Name]; duplicate {
			return fmt.Errorf("duplicate package path %q", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			return fmt.Errorf("directory entries are not allowed: %s", entry.Name)
		}
		if sensitivePath(entry.Name) {
			return fmt.Errorf("sensitive path is not allowed: %s", entry.Name)
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if containsPrivateKey(data) {
			return fmt.Errorf("private key material is not allowed: %s", entry.Name)
		}
		files[entry.Name] = data
	}
	manifestRaw, ok := files["manifest.json"]
	if !ok {
		return errors.New("manifest.json is missing")
	}
	signatureRaw, ok := files["signature.json"]
	if !ok {
		return errors.New("signature.json is missing")
	}
	var manifest struct {
		ID       string `json:"id"`
		Version  string `json:"version"`
		Runtimes map[string]struct {
			Path string `json:"path"`
		} `json:"runtimes"`
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return err
	}
	var signature struct {
		Algorithm string `json:"algorithm"`
		KeyID     string `json:"key_id"`
		PublicKey string `json:"public_key"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(signatureRaw, &signature); err != nil {
		return err
	}
	if signature.Algorithm != "ed25519" || signature.KeyID != *keyID || signature.PublicKey != base64.StdEncoding.EncodeToString(public) {
		return errors.New("signature publisher metadata does not match")
	}
	sig, err := base64.StdEncoding.DecodeString(signature.Signature)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(public), manifestRaw, sig) {
		return errors.New("manifest signature is invalid")
	}
	for target, runtime := range manifest.Runtimes {
		if runtime.Path == "" {
			return fmt.Errorf("runtime %s has no path", target)
		}
		data, ok := files[runtime.Path]
		if !ok {
			return fmt.Errorf("runtime file is missing: %s", runtime.Path)
		}
		if err := verifyRuntimeArchitecture(target, data); err != nil {
			return err
		}
	}
	for name, expected := range manifest.Files {
		data, ok := files[name]
		if !ok {
			return fmt.Errorf("manifest file is missing: %s", name)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != expected {
			return fmt.Errorf("file hash mismatch: %s", name)
		}
	}
	for name := range files {
		if name != "manifest.json" && name != "signature.json" {
			if _, declared := manifest.Files[name]; !declared {
				return fmt.Errorf("undeclared package file: %s", name)
			}
		}
	}
	fmt.Printf("verified id=%s version=%s files=%d runtimes=%d\n", manifest.ID, manifest.Version, len(manifest.Files), len(manifest.Runtimes))
	return nil
}

func verifyRuntimeArchitecture(target string, data []byte) error {
	switch target {
	case "windows-amd64":
		file, err := pe.NewFile(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("invalid windows-amd64 runtime: %w", err)
		}
		defer file.Close()
		if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
			return errors.New("windows-amd64 runtime has the wrong architecture")
		}
	case "linux-amd64", "linux-arm64":
		file, err := elf.NewFile(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("invalid %s runtime: %w", target, err)
		}
		defer file.Close()
		expected := elf.EM_X86_64
		if target == "linux-arm64" {
			expected = elf.EM_AARCH64
		}
		if file.Machine != expected {
			return fmt.Errorf("%s runtime has the wrong architecture", target)
		}
	default:
		return fmt.Errorf("unsupported runtime target %s", target)
	}
	return nil
}

func sensitivePath(name string) bool {
	lower := strings.ToLower(name)
	base := lower
	if index := strings.LastIndex(lower, "/"); index >= 0 {
		base = lower[index+1:]
	}
	if base == "auth.json" || base == "config.toml" || base == "runtime.json" || base == ".env" {
		return true
	}
	return strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".pfx") || strings.HasSuffix(base, ".p12")
}

func containsPrivateKey(data []byte) bool {
	text := strings.ToUpper(string(data))
	return strings.Contains(text, "-----BEGIN PRIVATE KEY-----") ||
		strings.Contains(text, "-----BEGIN OPENSSH PRIVATE KEY-----") ||
		strings.Contains(text, "-----BEGIN EC PRIVATE KEY-----") ||
		strings.Contains(text, "-----BEGIN RSA PRIVATE KEY-----")
}
