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

const AppVersion = "0.2.0"

type VersionInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	UpdateURL string `json:"update_url,omitempty"`
	Behind    bool   `json:"behind"`
	Repo      string `json:"repo,omitempty"`
}

var globalVersionInfo = VersionInfo{Current: AppVersion}

func checkForUpdates() {
	repo := strings.TrimSpace(os.Getenv("JANUS_UPDATE_REPO"))
	if repo == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "janus/"+AppVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return
	}
	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(AppVersion, "v")
	globalVersionInfo.Repo = repo
	globalVersionInfo.Latest = release.TagName
	globalVersionInfo.UpdateURL = release.HTMLURL
	globalVersionInfo.Behind = latest != current
	if globalVersionInfo.Behind {
		log.Printf("version: UPDATE AVAILABLE %s -> %s — %s", AppVersion, release.TagName, release.HTMLURL)
	}
}

func (s *server) handleVersion(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, globalVersionInfo)
}
