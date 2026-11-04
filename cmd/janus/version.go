package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// AppVersion is the current release. Bump this on every release so deployed
// instances can detect that an update is available.
const AppVersion = "0.2.0"

var (
	buildTime   = "dev"
	buildCommit = "local"
)

// VersionInfo is returned by GET /version and polled by the WebUI.
type VersionInfo struct {
	Current     string `json:"current"`
	BuildTime   string `json:"build_time,omitempty"`
	BuildCommit string `json:"build_commit,omitempty"`
	Latest      string `json:"latest,omitempty"`
	UpdateURL   string `json:"update_url,omitempty"`
	Behind      bool   `json:"behind"`
	Repo        string `json:"repo,omitempty"`
}

var globalVersionInfo = VersionInfo{
	Current:     AppVersion,
	BuildTime:   buildTime,
	BuildCommit: buildCommit,
}

// checkForUpdates queries the GitHub releases API for a newer version.
// Set JANUS_UPDATE_REPO=owner/repo to enable (e.g. "yourname/janus").
// Runs once at startup in a goroutine; result is cached in globalVersionInfo.
func checkForUpdates() {
	repo := strings.TrimSpace(os.Getenv("JANUS_UPDATE_REPO"))
	if repo == "" {
		log.Printf("version: update checks disabled (set JANUS_UPDATE_REPO=owner/repo to enable)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		log.Printf("version: update check error: %v", err)
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "janus/"+AppVersion)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("version: update check failed (network): %v", err)
		return
	}
	defer resp.Body.Close()

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		log.Printf("version: update check: bad response: %v", err)
		return
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(AppVersion, "v")

	globalVersionInfo.Repo = repo
	globalVersionInfo.Latest = release.TagName
	globalVersionInfo.UpdateURL = release.HTMLURL
	globalVersionInfo.Behind = latest != current

	if globalVersionInfo.Behind {
		log.Printf("⚠️  version: UPDATE AVAILABLE %s → %s — %s", AppVersion, release.TagName, release.HTMLURL)
	} else {
		log.Printf("version: up to date (%s)", AppVersion)
	}
}

func (r *router) handle_version(w http.ResponseWriter, req *http.Request) {
	write_json(w, http.StatusOK, globalVersionInfo)
}

