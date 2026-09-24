package license

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTierString(t *testing.T) {
	tests := []struct {
		tier Tier
		want string
	}{
		{TierCommunity, "community"},
		{TierProfessional, "professional"},
		{TierEnterprise, "enterprise"},
	}
	for _, tt := range tests {
		if got := tt.tier.String(); got != tt.want {
			t.Errorf("Tier(%d).String() = %q, want %q", tt.tier, got, tt.want)
		}
	}
}

func TestParseTier(t *testing.T) {
	tests := []struct {
		input string
		want  Tier
	}{
		{"professional", TierProfessional},
		{"ENTERPRISE", TierEnterprise},
		{"community", TierCommunity},
		{"unknown", TierCommunity},
		{"", TierCommunity},
	}
	for _, tt := range tests {
		if got := parseTier(tt.input); got != tt.want {
			t.Errorf("parseTier(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestAllowCommunity(t *testing.T) {
	lic := &License{valid: true, tier: TierCommunity}
	if lic.Allow(FeatureSwarm) {
		t.Error("community should not allow swarm")
	}
	if lic.Allow(FeatureCloudRouting) {
		t.Error("community should not allow cloud routing")
	}
	if lic.Allow(FeatureHIPAA) {
		t.Error("community should not allow HIPAA")
	}
}

func TestAllowProfessional(t *testing.T) {
	lic := &License{valid: true, tier: TierProfessional}
	if !lic.Allow(FeatureSwarm) {
		t.Error("professional should allow swarm")
	}
	if !lic.Allow(FeatureCloudRouting) {
		t.Error("professional should allow cloud routing")
	}
	if lic.Allow(FeatureHIPAA) {
		t.Error("professional should not allow HIPAA")
	}
}

func TestAllowEnterprise(t *testing.T) {
	lic := &License{valid: true, tier: TierEnterprise}
	if !lic.Allow(FeatureSwarm) {
		t.Error("enterprise should allow swarm")
	}
	if lic.Allow(FeatureHIPAA) {
		t.Error("OSS build does not expose HIPAA feature")
	}
	if !lic.Allow(FeatureUnlimitedSeats) {
		t.Error("enterprise should allow unlimited seats")
	}
}

func TestExpired(t *testing.T) {
	lic := &License{valid: true, tier: TierEnterprise, Payload: Payload{Expires: "2020-01-01"}}
	if !lic.Expired() {
		t.Error("license with past date should be expired")
	}
	if lic.Allow(FeatureSwarm) {
		t.Error("expired license should not allow swarm")
	}
}

func TestNotExpired(t *testing.T) {
	lic := &License{valid: true, tier: TierEnterprise, Payload: Payload{Expires: "2099-12-31"}}
	if lic.Expired() {
		t.Error("license with future date should not be expired")
	}
}

func TestNeverExpires(t *testing.T) {
	lic := &License{valid: true, tier: TierEnterprise, Payload: Payload{Expires: ""}}
	if lic.Expired() {
		t.Error("license with empty expires should never expire")
	}
}

func TestGenerateAndLoad(t *testing.T) {
	secret := "test-secret-key"
	SetSigningSecret(secret)
	defer SetSigningSecret("janus-license-secret-CHANGE-AT-BUILD-TIME")

	p := Payload{
		Licensee: "Test Clinic",
		Tier:     "professional",
		Issued:   "2026-01-01",
		Expires:  "2099-01-01",
		Seats:    5,
	}

	keyData, err := Generate(p, secret)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "license.key")
	if err := os.WriteFile(path, []byte(keyData), 0o644); err != nil {
		t.Fatal(err)
	}

	lic, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !lic.Valid() {
		t.Error("loaded license should be valid")
	}
	if lic.Tier() != TierProfessional {
		t.Errorf("tier = %s, want professional", lic.Tier())
	}
	if lic.Payload.Licensee != "Test Clinic" {
		t.Errorf("licensee = %q, want 'Test Clinic'", lic.Payload.Licensee)
	}
}

func TestLoadMissingFile(t *testing.T) {
	lic, err := Load(filepath.Join(t.TempDir(), "nonexistent.key"))
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if lic.Tier() != TierProfessional {
		t.Errorf("missing file should give open-source professional tier, got %s", lic.Tier())
	}
}

func TestLoadTamperedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.key")
	os.WriteFile(path, []byte(`{"body":"dGVzdA==","sig":"0000000000000000000000000000000000000000000000000000000000000000"}`), 0o644)

	_, err := Load(path)
	if err == nil {
		t.Error("expected error for tampered key")
	}
}

func TestNilLicense(t *testing.T) {
	var lic *License
	if lic.Valid() {
		t.Error("nil license should not be valid")
	}
	if lic.Tier() != TierCommunity {
		t.Errorf("nil license tier = %s, want community", lic.Tier())
	}
	if lic.Expired() {
		t.Error("nil license should not be expired")
	}
}

