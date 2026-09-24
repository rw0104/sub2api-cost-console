package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// CanonicalPackageProvenanceSchema identifies the sidecar format written by
// the release pipeline. The sidecar stays outside the ZIP because a package
// hash embedded in the package would be self-referential.
const CanonicalPackageProvenanceSchema = "sub2api.plugin.provenance/v1"

const canonicalPackageProvenanceMaxBytes = 1024 * 1024

var canonicalRuntimeKeyPattern = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)
var canonicalCommitPattern = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

// PluginPackageTestEvidence records a single release-gate result. A skipped
// check is explicit evidence and is never treated as a pass by the host.
type PluginPackageTestEvidence struct {
	Name     string `json:"name"`
	Result   string `json:"result"`
	Workflow string `json:"workflow,omitempty"`
	RunID    string `json:"run_id,omitempty"`
	Command  string `json:"command,omitempty"`
}

// PluginPackageProvenance is an external, signed-release metadata record for
// a .s2plugin artifact. It binds the exact bytes and selected runtime to the
// publisher identity and the build/test evidence used to publish it.
type PluginPackageProvenance struct {
	Schema               string                      `json:"schema"`
	PluginID             string                      `json:"plugin_id"`
	Version              string                      `json:"version"`
	RuntimeKey           string                      `json:"runtime_key"`
	PackageSHA256        string                      `json:"package_sha256"`
	BinarySHA256         string                      `json:"binary_sha256"`
	SignatureKeyID       string                      `json:"signature_key_id"`
	AttestationAlgorithm string                      `json:"attestation_algorithm"`
	AttestationSignature string                      `json:"attestation_signature"`
	PublisherFingerprint string                      `json:"publisher_fingerprint"`
	SourceCommit         string                      `json:"source_commit"`
	BuilderVersion       string                      `json:"builder_version"`
	TestEvidence         []PluginPackageTestEvidence `json:"test_evidence"`
	PublishedAt          time.Time                   `json:"published_at"`
}

// Validate checks fields that are independent of a particular plugin package.
func (p PluginPackageProvenance) Validate() error {
	if p.Schema != CanonicalPackageProvenanceSchema {
		return fmt.Errorf("不支持的插件 provenance schema: %q", p.Schema)
	}
	if strings.TrimSpace(p.PluginID) == "" || len(p.PluginID) > 160 {
		return errors.New("插件 provenance 缺少有效 plugin_id")
	}
	if strings.TrimSpace(p.Version) == "" || normalizeSemver(p.Version) == "" {
		return errors.New("插件 provenance 缺少有效 version")
	}
	if !canonicalRuntimeKeyPattern.MatchString(p.RuntimeKey) {
		return fmt.Errorf("插件 provenance runtime_key 无效: %q", p.RuntimeKey)
	}
	for name, value := range map[string]string{
		"package_sha256":        p.PackageSHA256,
		"binary_sha256":         p.BinarySHA256,
		"publisher_fingerprint": strings.TrimPrefix(p.PublisherFingerprint, "sha256:"),
	} {
		if !isCanonicalHexDigest(value, 32) {
			return fmt.Errorf("插件 provenance %s 必须是小写 SHA-256", name)
		}
	}
	if !strings.HasPrefix(p.PublisherFingerprint, "sha256:") {
		return errors.New("插件 provenance publisher_fingerprint 必须使用 sha256: 前缀")
	}
	if p.AttestationAlgorithm != "ed25519" {
		return errors.New("插件 provenance attestation_algorithm 必须是 ed25519")
	}
	attestation, err := base64.StdEncoding.DecodeString(p.AttestationSignature)
	if err != nil || len(attestation) != ed25519.SignatureSize {
		return errors.New("插件 provenance attestation_signature 无效")
	}
	if !canonicalCommitPattern.MatchString(p.SourceCommit) {
		return errors.New("插件 provenance source_commit 必须是 40–64 位小写提交哈希")
	}
	if strings.TrimSpace(p.BuilderVersion) == "" || len(p.BuilderVersion) > 128 || containsControl(p.BuilderVersion) {
		return errors.New("插件 provenance builder_version 无效")
	}
	if p.PublishedAt.IsZero() {
		return errors.New("插件 provenance 缺少 published_at")
	}
	if len(p.TestEvidence) == 0 {
		return errors.New("插件 provenance 至少需要一条 test_evidence")
	}
	for index, evidence := range p.TestEvidence {
		if strings.TrimSpace(evidence.Name) == "" || len(evidence.Name) > 160 || containsControl(evidence.Name) {
			return fmt.Errorf("插件 provenance test_evidence[%d] 缺少有效 name", index)
		}
		switch evidence.Result {
		case "passed", "skipped":
		default:
			return fmt.Errorf("插件 provenance test_evidence[%d] result 必须是 passed 或 skipped", index)
		}
		for field, value := range map[string]string{
			"workflow": evidence.Workflow,
			"run_id":   evidence.RunID,
			"command":  evidence.Command,
		} {
			if len(value) > 512 || containsControl(value) {
				return fmt.Errorf("插件 provenance test_evidence[%d].%s 无效", index, field)
			}
		}
	}
	return nil
}

// ValidateAgainst binds provenance to the host's already verified package
// inspection. It is deliberately separate from Validate so callers cannot
// accidentally accept metadata that describes a different artifact.
func (p PluginPackageProvenance) ValidateAgainst(
	manifest PluginManifest,
	packageSHA256 string,
	runtimeKey string,
	binarySHA256 string,
	publisher *PluginPublisher,
) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if manifest.ID != p.PluginID || manifest.Version != p.Version {
		return fmt.Errorf("插件 provenance 与清单身份不一致: %s@%s", manifest.ID, manifest.Version)
	}
	if runtimeKey != p.RuntimeKey {
		return fmt.Errorf("插件 provenance runtime_key 不匹配: %s", runtimeKey)
	}
	if !strings.EqualFold(packageSHA256, p.PackageSHA256) || strings.ToLower(packageSHA256) != packageSHA256 {
		return errors.New("插件 provenance package_sha256 与实际包不一致")
	}
	entry, ok := manifest.Runtimes[runtimeKey]
	if !ok || manifest.Files[entry.Path] != p.BinarySHA256 || binarySHA256 != p.BinarySHA256 {
		return errors.New("插件 provenance binary_sha256 与清单不一致")
	}
	if publisher == nil {
		return errors.New("canonical 插件包必须具备已验证的发布者签名")
	}
	if p.SignatureKeyID != publisher.KeyID || p.PublisherFingerprint != publisher.Fingerprint {
		return errors.New("插件 provenance 发布者签名身份不一致")
	}
	publicKey, err := base64.StdEncoding.DecodeString(publisher.PublicKey)
	attestation, attestationErr := base64.StdEncoding.DecodeString(p.AttestationSignature)
	if err != nil || len(publicKey) != ed25519.PublicKeySize || attestationErr != nil || len(attestation) != ed25519.SignatureSize ||
		!ed25519.Verify(ed25519.PublicKey(publicKey), canonicalProvenanceSigningBytes(p), attestation) {
		return errors.New("插件 provenance attestation 签名校验失败")
	}
	return nil
}

// canonicalProvenanceSigningBytes returns the stable JSON payload signed by a
// release attestation. The signature itself is cleared before encoding, so
// the payload is deterministic and cannot be self-referential.
func canonicalProvenanceSigningBytes(p PluginPackageProvenance) []byte {
	p.AttestationSignature = ""
	raw, _ := json.Marshal(p)
	return raw
}

// SignCanonicalPackageProvenance signs a sidecar in release tooling. The
// private key is never retained by the host; installation only verifies the
// resulting signature with the package publisher's public key.
func SignCanonicalPackageProvenance(p *PluginPackageProvenance, privateKey ed25519.PrivateKey) error {
	if p == nil || len(privateKey) != ed25519.PrivateKeySize {
		return errors.New("插件 provenance 签名密钥无效")
	}
	p.AttestationAlgorithm = "ed25519"
	p.AttestationSignature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonicalProvenanceSigningBytes(*p)))
	return nil
}

// ParseCanonicalPackageProvenance parses one bounded JSON sidecar. Unknown
// fields are rejected so release tooling cannot silently emit misspelled
// evidence fields.
func ParseCanonicalPackageProvenance(reader io.Reader) (*PluginPackageProvenance, error) {
	if reader == nil {
		return nil, errors.New("插件 provenance 为空")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, canonicalPackageProvenanceMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取插件 provenance: %w", err)
	}
	if len(raw) > canonicalPackageProvenanceMaxBytes {
		return nil, errors.New("插件 provenance 超过大小限制")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var provenance PluginPackageProvenance
	if err := decoder.Decode(&provenance); err != nil {
		return nil, fmt.Errorf("解析插件 provenance: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("插件 provenance 只能包含一个 JSON 对象")
	}
	if err := provenance.Validate(); err != nil {
		return nil, err
	}
	return &provenance, nil
}

// InspectCanonical verifies a package and then binds it to an external
// canonical provenance sidecar. Existing Inspect callers retain legacy
// behavior and do not require the sidecar.
func (i *PluginPackageInstaller) InspectCanonical(ctx context.Context, packageReader, provenanceReader io.Reader) (*PluginPackageInspection, error) {
	inspection, err := i.Inspect(ctx, packageReader)
	if err != nil {
		return nil, err
	}
	provenance, err := ParseCanonicalPackageProvenance(provenanceReader)
	if err != nil {
		return nil, err
	}
	runtimeKey := i.runtimeKey(inspection.Manifest)
	runtimeEntry := inspection.Manifest.Runtimes[runtimeKey]
	if err := provenance.ValidateAgainst(inspection.Manifest, inspection.PackageSHA256, runtimeKey, inspection.Manifest.Files[runtimeEntry.Path], inspection.Publisher); err != nil {
		return nil, err
	}
	inspection.CanonicalProvenance = provenance
	return inspection, nil
}

// InstallCanonicalWithApproval is the strict installation entry point for a
// release artifact. It validates the sidecar before persisting or extracting
// any files, then reuses the existing transactional installer.
func (i *PluginPackageInstaller) InstallCanonicalWithApproval(
	ctx context.Context,
	packageReader io.Reader,
	provenanceReader io.Reader,
	installedBy *int64,
	approval *PluginPublisherApproval,
) (*PluginInstallation, error) {
	if i == nil || i.cfg == nil || i.cfg.Plugins.MaxUploadBytes <= 0 {
		return nil, errors.New("插件安装器未配置")
	}
	if packageReader == nil || provenanceReader == nil {
		return nil, errors.New("canonical 插件包和 provenance 均不能为空")
	}
	packageData, err := io.ReadAll(io.LimitReader(packageReader, i.cfg.Plugins.MaxUploadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取插件包: %w", err)
	}
	if int64(len(packageData)) > i.cfg.Plugins.MaxUploadBytes {
		return nil, errors.New("插件包超过上传大小限制")
	}
	if _, err := i.InspectCanonical(ctx, bytes.NewReader(packageData), provenanceReader); err != nil {
		return nil, err
	}
	return i.InstallWithApproval(ctx, bytes.NewReader(packageData), installedBy, approval)
}

func isCanonicalHexDigest(value string, bytesLength int) bool {
	if len(value) != bytesLength*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == bytesLength
}

func containsControl(value string) bool {
	return strings.ContainsAny(value, "\x00\r\n")
}
