package registry

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Peer is another Adeptus tool registered on this PC.
type Peer struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	Name    string `json:"name,omitempty"`
	Kind    string `json:"kind,omitempty"`
}

// ListPeers reads registry JSON files except skipID (usually "janus").
func ListPeers(skipID string) []Peer {
	dir := Dir()
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Peer
	client := &http.Client{Timeout: 2 * time.Second}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if id == skipID {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var raw Entry
		if err := json.Unmarshal(b, &raw); err != nil {
			continue
		}
		p := Peer{ID: raw.ID, Address: raw.Address}
		base := strings.TrimSuffix(strings.TrimSpace(raw.Address), "/")
		base = strings.TrimSuffix(base, "/v1")
		healthURL := base + "/health"
		if len(raw.Card) > 0 {
			var card struct {
				Name    string `json:"name"`
				Version string `json:"version"`
				Kind    string `json:"kind"`
				Health  string `json:"health"`
			}
			_ = json.Unmarshal(raw.Card, &card)
			p.Name = card.Name
			p.Version = card.Version
			p.Kind = card.Kind
			if card.Health != "" && strings.HasPrefix(card.Health, "/") {
				healthURL = base + card.Health
			}
			req, _ := http.NewRequest(http.MethodGet, healthURL, nil)
			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				p.OK = true
				var h struct {
					Version string `json:"version"`
					ID      string `json:"id"`
				}
				_ = json.Unmarshal(body, &h)
				if h.Version != "" {
					p.Version = h.Version
				}
			}
		}
		out = append(out, p)
	}
	return out
}
