package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCanonicalPluginPackageProvenanceBindsInspection(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	cfg := testPluginConfig(t.TempDir(), false)
	cfg.Plugins.TrustedPublishers["canonical-test"] = base64.StdEncoding.EncodeToString(publicKey)
	installer := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.1.179"})
	archive := buildTestPluginArchive(t, privateKey, "canonical-test")

	inspection, err := installer.Inspect(context.Background(), bytes.NewReader(archive))
	require.NoError(t, err)
	require.NotNil(t, inspection.Publisher)
	runtimeKey := installer.runtimeKey(inspection.Manifest)
	runtimePath := inspection.Manifest.Runtimes[runtimeKey].Path
	provenance := PluginPackageProvenance{
		Schema:               CanonicalPackageProvenanceSchema,
		PluginID:             inspection.Manifest.ID,
		Version:              inspection.Manifest.Version,
		RuntimeKey:           runtimeKey,
		PackageSHA256:        inspection.PackageSHA256,
		BinarySHA256:         inspection.Manifest.Files[runtimePath],
		SignatureKeyID:       inspection.Publisher.KeyID,
		PublisherFingerprint: inspection.Publisher.Fingerprint,
		SourceCommit:         "0123456789abcdef0123456789abcdef01234567",
		BuilderVersion:       "go1.27.0; packer 1.0",
		TestEvidence: []PluginPackageTestEvidence{{
			Name: "host-package-smoke", Result: "passed", Workflow: "plugin-canonical", RunID: "123",
		}},
		PublishedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	require.NoError(t, SignCanonicalPackageProvenance(&provenance, privateKey))

	raw, err := json.Marshal(provenance)
	require.NoError(t, err)
	canonicalInspection, err := installer.InspectCanonical(context.Background(), bytes.NewReader(archive), bytes.NewReader(raw))
	require.NoError(t, err)
	require.Equal(t, provenance.PackageSHA256, canonicalInspection.CanonicalProvenance.PackageSHA256)

	mutated := provenance
	mutated.SourceCommit = "fedcba9876543210fedcba9876543210fedcba98"
	mutatedRaw, err := json.Marshal(mutated)
	require.NoError(t, err)
	_, err = installer.InspectCanonical(context.Background(), bytes.NewReader(archive), bytes.NewReader(mutatedRaw))
	require.ErrorContains(t, err, "attestation 签名")

	provenance.PackageSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	raw, err = json.Marshal(provenance)
	require.NoError(t, err)
	_, err = installer.InspectCanonical(context.Background(), bytes.NewReader(archive), bytes.NewReader(raw))
	require.ErrorContains(t, err, "package_sha256")
}

func TestCanonicalInstallerRejectsFailedEvidenceAndAcceptsVerifiedArtifact(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	cfg := testPluginConfig(t.TempDir(), false)
	cfg.Plugins.TrustedPublishers["canonical-install"] = base64.StdEncoding.EncodeToString(publicKey)
	installer := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.1.179"})
	archive := buildTestPluginArchive(t, privateKey, "canonical-install")
	inspection, err := installer.Inspect(context.Background(), bytes.NewReader(archive))
	require.NoError(t, err)
	runtimeKey := installer.runtimeKey(inspection.Manifest)
	runtimePath := inspection.Manifest.Runtimes[runtimeKey].Path
	provenance := PluginPackageProvenance{
		Schema: CanonicalPackageProvenanceSchema, PluginID: inspection.Manifest.ID,
		Version: inspection.Manifest.Version, RuntimeKey: runtimeKey,
		PackageSHA256: inspection.PackageSHA256, BinarySHA256: inspection.Manifest.Files[runtimePath],
		SignatureKeyID: inspection.Publisher.KeyID, PublisherFingerprint: inspection.Publisher.Fingerprint,
		SourceCommit: "fedcba9876543210fedcba9876543210fedcba98", BuilderVersion: "go1.27.0",
		TestEvidence: []PluginPackageTestEvidence{{Name: "required-smoke", Result: "passed"}},
		PublishedAt:  time.Unix(1_700_000_000, 0).UTC(),
	}
	require.NoError(t, SignCanonicalPackageProvenance(&provenance, privateKey))

	failed := provenance
	failed.TestEvidence = []PluginPackageTestEvidence{{Name: "required-smoke", Result: "failed"}}
	failedRaw, err := json.Marshal(failed)
	require.NoError(t, err)
	_, err = installer.InstallCanonicalWithApproval(context.Background(), bytes.NewReader(archive), bytes.NewReader(failedRaw), nil, nil)
	require.ErrorContains(t, err, "result 必须是 passed 或 skipped")

	raw, err := json.Marshal(provenance)
	require.NoError(t, err)
	installed, err := installer.InstallCanonicalWithApproval(context.Background(), bytes.NewReader(archive), bytes.NewReader(raw), nil, nil)
	require.NoError(t, err)
	require.Equal(t, PluginSignatureTrusted, installed.SignatureStatus)
}

func TestCanonicalProvenanceRejectsUnknownFieldsAndInvalidEvidence(t *testing.T) {
	_, err := ParseCanonicalPackageProvenance(bytes.NewBufferString(`{"schema":"sub2api.plugin.provenance/v1","unexpected":true}`))
	require.ErrorContains(t, err, "解析插件 provenance")

	provenance := PluginPackageProvenance{
		Schema: CanonicalPackageProvenanceSchema, PluginID: "com.example.plugin", Version: "1.0.0",
		RuntimeKey: "windows-amd64", PackageSHA256: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		BinarySHA256:         "0000000000000000000000000000000000000000000000000000000000000000",
		PublisherFingerprint: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		SourceCommit:         "0123456789abcdef0123456789abcdef01234567", BuilderVersion: "go1.27.0",
		TestEvidence: []PluginPackageTestEvidence{{Name: "smoke", Result: "skipped"}},
		PublishedAt:  time.Unix(1_700_000_000, 0).UTC(),
	}
	provenance.AttestationAlgorithm = "ed25519"
	provenance.AttestationSignature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	require.ErrorContains(t, provenance.Validate(), "小写 SHA-256")
}
