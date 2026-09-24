// Package license implements an offline, air-gapped license key system for
// Janus.  Keys are signed JSON payloads verified with HMAC-SHA256.
//
// Tiers (in ascending capability order):
//
//	community    — single-user local inference only, no cloud, no swarm
//	professional — +cloud routing, +multi-agent swarm (up to 5 agents)
//	enterprise   — all features, unlimited agents (enterprise-only compliance removed in OSS)
//
// Key format (JSON, base64-encoded body + hex HMAC tag):
//
//	{
//	  "body": "<base64(JSON payload)>",
//	  "sig":  "<hex(HMAC-SHA256(body, secret))"
//	}
//
// Payload JSON:
//
//	{
//	  "licensee": "Acme Clinic",
//	  "tier":     "enterprise",
//	  "issued":   "2026-01-01",
//	  "expires":  "2027-01-01",   // "" = never
//	  "seats":    10
//	}
package license

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// Tier represents a license capability level.
type Tier int

const (
	TierCommunity    Tier = 0
	TierProfessional Tier = 1
	TierEnterprise   Tier = 2
)

func (t Tier) String() string {
	switch t {
	case TierProfessional:
		return "professional"
	case TierEnterprise:
		return "enterprise"
	default:
		return "community"
	}
}

// parseTier converts a string tier name to Tier.
func parseTier(s string) Tier {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "professional":
		return TierProfessional
	case "enterprise":
		return TierEnterprise
	default:
		return TierCommunity
	}
}

// Feature constants used with Allow().
const (
	FeatureCloudRouting   = "cloud_routing"
	FeatureSwarm          = "swarm"
	FeatureHIPAA          = "hipaa"
	FeatureUnlimitedSeats = "unlimited_seats"
)

// Payload is the inner license content.
type Payload struct {
	Licensee string `json:"licensee"`
	Tier     string `json:"tier"`
	Issued   string `json:"issued"`
	Expires  string `json:"expires"` // "" = never expires
	Seats    int    `json:"seats"`   // 0 = unlimited
}

// License is a loaded and verified license.
type License struct {
	Payload Payload
	tier    Tier
	valid   bool
}

// Valid returns true if the license was loaded and verified successfully.
func (l *License) Valid() bool { return l != nil && l.valid }

// Tier returns the license tier.
func (l *License) Tier() Tier {
	if l == nil {
		return TierCommunity
	}
	return l.tier
}

// Expired returns true if the license has passed its expiry date.
func (l *License) Expired() bool {
	if l == nil || l.Payload.Expires == "" {
		return false
	}
	exp, err := time.Parse("2006-01-02", l.Payload.Expires)
	if err != nil {
		return false
	}
	return time.Now().UTC().After(exp)
}

// Allow returns true when the current license tier permits the given feature.
func (l *License) Allow(feature string) bool {
	if l == nil || !l.valid || l.Expired() {
		// Community defaults: local inference only.
		return feature != FeatureCloudRouting &&
			feature != FeatureSwarm &&
			feature != FeatureHIPAA &&
			feature != FeatureUnlimitedSeats
	}
	switch feature {
	case FeatureCloudRouting:
		return l.tier >= TierProfessional
	case FeatureSwarm:
		return l.tier >= TierProfessional
	case FeatureHIPAA:
		return false
	case FeatureUnlimitedSeats:
		return l.tier >= TierEnterprise || l.Payload.Seats == 0
	}
	return true
}

// wireFormat is the on-disk JSON structure.
type wireFormat struct {
	Body string `json:"body"`
	Sig  string `json:"sig"`
}

// signingSecret is the HMAC key used to verify licenses.
// In production this should be injected at build time via -ldflags.
// Placeholder default ensures the zero-install community tier works without a key.
var signingSecret = "janus-license-secret-CHANGE-AT-BUILD-TIME"

// SetSigningSecret overrides the HMAC secret (called from main with build-time flag).
func SetSigningSecret(s string) {
	if s != "" {
		signingSecret = s
	}
}

// verify checks the HMAC tag on a raw body string.
func verify(body, sigHex string) bool {
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(body))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sigHex))
}

// Load reads and verifies a license file.  Returns a community license if the
// file is absent, and an error if the file is present but invalid.
func Load(path string) (*License, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		log.Printf("license: no key file at %s — running open-source Professional tier", path)
		return &License{
			valid: true,
			tier:  TierProfessional,
			Payload: Payload{
				Licensee: "open-source",
				Tier:     "professional",
			},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("license: read %s: %w", path, err)
	}

	var wf wireFormat
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("license: parse key file: %w", err)
	}

	if !verify(wf.Body, wf.Sig) {
		return nil, fmt.Errorf("license: HMAC signature invalid — key file may be tampered")
	}

	rawPayload, err := base64.StdEncoding.DecodeString(wf.Body)
	if err != nil {
		return nil, fmt.Errorf("license: decode payload: %w", err)
	}

	var p Payload
	if err := json.Unmarshal(rawPayload, &p); err != nil {
		return nil, fmt.Errorf("license: unmarshal payload: %w", err)
	}

	lic := &License{
		Payload: p,
		tier:    parseTier(p.Tier),
		valid:   true,
	}

	if lic.Expired() {
		log.Printf("license: key for %q expired on %s — downgrading to Community", p.Licensee, p.Expires)
		lic.tier = TierCommunity
	} else {
		log.Printf("license: loaded %s tier for %q (expires: %s, seats: %d)",
			lic.tier, p.Licensee, orNever(p.Expires), p.Seats)
	}

	return lic, nil
}

// Generate creates a signed license key file.  Useful for the license-issuer
// CLI tool; not exposed at runtime.
func Generate(p Payload, secret string) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	body := base64.StdEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	sig := hex.EncodeToString(mac.Sum(nil))
	out, err := json.MarshalIndent(wireFormat{Body: body, Sig: sig}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func orNever(s string) string {
	if s == "" {
		return "never"
	}
	return s
}

// ─── Global singleton ─────────────────────────────────────────────────────────

var (
	globalOnce sync.Once
	globalLic  *License
	licMu      sync.RWMutex
)

// Init loads the license once at startup from the given path.
func Init(path string) {
	globalOnce.Do(func() {
		lic, err := Load(path)
		if err != nil {
			log.Printf("license: WARNING — %v — falling back to open-source Professional tier", err)
			lic = &License{
				valid: true,
				tier:  TierProfessional,
				Payload: Payload{
					Licensee: "open-source",
					Tier:     "professional",
				},
			}
		}
		licMu.Lock()
		globalLic = lic
		licMu.Unlock()
	})
}

// Reload re-reads and re-verifies the license key from the given path at runtime.
func Reload(path string) error {
	lic, err := Load(path)
	if err != nil {
		return err
	}
	licMu.Lock()
	globalLic = lic
	licMu.Unlock()
	return nil
}

// Get returns the globally loaded license.  Always non-nil after Init().
func Get() *License {
	licMu.RLock()
	defer licMu.RUnlock()
	if globalLic == nil {
		return &License{valid: true, tier: TierCommunity}
	}
	return globalLic
}

// Allow is a convenience wrapper: license.Allow(feature).
func Allow(feature string) bool { return Get().Allow(feature) }


