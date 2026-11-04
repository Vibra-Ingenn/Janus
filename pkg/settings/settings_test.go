package settings

import (
	"os"
	"testing"
)

func TestApplyRespectsExistingEnv(t *testing.T) {
	t.Setenv("JANUS_LISTEN_ADDR", "127.0.0.1:8996")
	f := &File{Values: map[string]string{
		"JANUS_LISTEN_ADDR": "127.0.0.1:8990",
	}}
	f.Apply()
	if got := os.Getenv("JANUS_LISTEN_ADDR"); got != "127.0.0.1:8996" {
		t.Fatalf("Apply overwrote env: got %q", got)
	}
}
