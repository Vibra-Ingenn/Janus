package main

import (
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"janus/pkg/singleton"
)

func consoleURL(listenAddr string) string {
	return singleton.BrowserBaseURL(listenAddr) + "/"
}

func openBrowserAt(url string) {
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	log.Printf("opening browser at %s", url)
	var err error
	if runtime.GOOS == "windows" {
		err = exec.Command("cmd", "/c", "start", "", url).Start()
		if err != nil {
			log.Printf("janus: cmd start failed: %v", err)
			err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
		}
	} else if runtime.GOOS == "darwin" {
		err = exec.Command("open", url).Start()
	} else {
		err = exec.Command("xdg-open", url).Start()
	}
	if err != nil {
		log.Printf("janus: could not open browser: %v", err)
	}
}

// shouldOpenBrowser is true unless JANUS_NO_BROWSER disables it (tests and automation set 1).
func shouldOpenBrowser() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_NO_BROWSER")))
	switch v {
	case "1", "true", "yes", "on":
		return false
	case "0", "false", "no", "off":
		return true
	default:
		return v == ""
	}
}

func handlePortBeforeStart(listenAddr string) (proceed bool) {
	free, busy := singleton.CheckPortBeforeListen(listenAddr)
	url := consoleURL(listenAddr)
	if free {
		return true
	}
	if busy != nil && busy.HoldingJanus {
		// A rebuilt janus.exe replaces the older copy of itself that's still running.
		if singleton.ReplaceIfStale(consoleURL(listenAddr), listenAddr) {
			log.Print("Replaced the older running copy of Janus with this newer build.")
			return true
		}
		// No pop-up: double-clicking while Janus runs just brings its page up again.
		log.Print("Janus is already running; opening its page.")
		if shouldOpenBrowser() {
			openBrowserAt(url)
		}
		return false
	}
	who := "another program"
	if busy != nil {
		if busy.ProcessName != "" {
			who = busy.ProcessName
		}
		if busy.PID != "" {
			who += " (PID " + busy.PID + ")"
		}
	}
	msg := "Port " + portFromAddr(listenAddr) + " is already in use by " + who + ", not Janus.\nClose that program or change JANUS_LISTEN_ADDR in Settings, then start Janus again."
	log.Print(msg)
	userAlertAndWait("Janus", msg)
	return false
}

func portFromAddr(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i+1:]
	}
	return addr
}
