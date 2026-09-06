// Package shell provides safe command execution via WSL2, including a
// multi-layer blacklist designed to resist creative bypass attempts.
package shell

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// dangerPattern is a compiled regex with a human-readable reason for blocking.
type dangerPattern struct {
	re     *regexp.Regexp
	reason string
}

// hardBlockedPatterns are checked against the normalized command.
// These are ABSOLUTE blocks — no exception, no override.
var hardBlockedPatterns = []dangerPattern{
	// --- Recursive root/system deletion ---
	{regexp.MustCompile(`\brm\b.*-[a-z]*r[a-z]*.*-[a-z]*f[a-z]*\s+/`), "rm -rf on root or system path"},
	{regexp.MustCompile(`\brm\b.*-[a-z]*f[a-z]*.*-[a-z]*r[a-z]*\s+/`), "rm -fr on root or system path"},
	{regexp.MustCompile(`\brm\b.*-rf\s*/*\s*$`), "rm -rf with bare slash"},
	{regexp.MustCompile(`\brm\b.*--no-preserve-root`), "rm --no-preserve-root override"},

	// --- Fork bomb ---
	{regexp.MustCompile(`:\s*\(\s*\)\s*\{[^}]*\|\s*:(\s*&[^;]*)?[;}]`), "fork bomb pattern"},

	// --- Raw disk / filesystem wipe ---
	{regexp.MustCompile(`\bdd\b.*of=/dev/(sd|nvme|vd|hd|xvd|mmcblk)[a-z0-9]`), "dd to raw disk device"},
	{regexp.MustCompile(`\b(mkfs|mke2fs|mkntfs|mkswap|mkdosfs)\b`), "filesystem formatter"},
	{regexp.MustCompile(`\bwipefs\b`), "wipe filesystem signatures"},
	{regexp.MustCompile(`\bshred\b.*(-[a-z]*u[a-z]*|-[a-z]*z[a-z]*)`), "shred with secure-erase flags"},

	// --- System halt / reboot ---
	{regexp.MustCompile(`\b(halt|poweroff|reboot|shutdown|systemctl (poweroff|reboot|halt))\b`), "system halt or reboot"},
	{regexp.MustCompile(`\binit\s+[06]\b`), "init 0/6 runlevel (shutdown/reboot)"},

	// --- Privilege escalation ---
	{regexp.MustCompile(`\bsudo\s+(su|bash|sh|zsh|fish|dash|rbash)\b`), "privilege escalation via sudo shell"},
	{regexp.MustCompile(`\bsu\s+-[a-z]*\s*root\b`), "su to root"},
	{regexp.MustCompile(`\bchmod\s+[0-7]*7[0-7]*\s+/(etc|bin|sbin|usr|boot|lib)`), "chmod on protected system directory"},
	{regexp.MustCompile(`\bchown\s+\S+\s+/(etc|bin|sbin|usr|boot|lib)`), "chown on protected system directory"},

	// --- Writing to /etc (config hijack) ---
	{regexp.MustCompile(`(>>?|tee(\s+-a)?)\s+/etc/`), "write or append to /etc"},
	{regexp.MustCompile(`\bcp\b.*\s+/etc/`), "copy file into /etc"},

	// --- Network exfiltration / remote code execution ---
	{regexp.MustCompile(`\bcurl\b.*\|\s*(bash|sh|zsh|python3?|perl|ruby|node)\b`), "curl piped to shell/interpreter"},
	{regexp.MustCompile(`\bwget\b.*-O\s*-\s*\|`), "wget output piped to command"},
	{regexp.MustCompile(`\bnc\b.*(-e|-c)\b`), "netcat exec mode"},
	{regexp.MustCompile(`/dev/tcp/`), "bash /dev/tcp redirect (reverse shell)"},
	{regexp.MustCompile(`\bsocat\b.*exec`), "socat exec (reverse shell)"},

	// --- Encoding / obfuscation bypasses ---
	{regexp.MustCompile(`\bbase64\b.*-d.*\|`), "base64 decode piped to command"},
	{regexp.MustCompile(`\beval\b`), "eval (arbitrary code execution)"},
	{regexp.MustCompile(`\bexec\b\s+[^>]`), "exec replacing current process"},
	{regexp.MustCompile(`\$\(\s*(base64|openssl|xxd|od)`), "command substitution with decoder"},

	// --- Kernel / module tampering ---
	{regexp.MustCompile(`\b(insmod|rmmod|modprobe)\b`), "kernel module load/unload"},
	{regexp.MustCompile(`\bsysctl\s+-w\b`), "sysctl write (kernel param change)"},
	{regexp.MustCompile(`echo\s+.*>\s*/proc/sys/`), "write to /proc/sys"},

	// --- Persistence mechanisms ---
	{regexp.MustCompile(`\bcrontab\s+-[^l]`), "crontab modification (not -l list)"},
	{regexp.MustCompile(`\bsystemctl\s+(enable|disable|mask|unmask)\b`), "systemd service modification"},
	{regexp.MustCompile(`(>>?|tee)\s+~?/\.(bashrc|zshrc|profile|bash_profile|bash_login)`), "shell rc file modification"},

	// --- LD_PRELOAD / library injection ---
	{regexp.MustCompile(`\bLD_PRELOAD\s*=`), "LD_PRELOAD injection"},
	{regexp.MustCompile(`\bLD_LIBRARY_PATH\s*=.*/tmp`), "LD_LIBRARY_PATH pointing to /tmp"},
}

// protectedPaths are filesystem paths that destructive operations
// (rm, mv, cp overwrite, truncate) may never target.
var protectedPaths = []string{
	"/",
	"/etc",
	"/bin",
	"/sbin",
	"/usr",
	"/boot",
	"/lib",
	"/lib64",
	"/proc",
	"/sys",
	"/dev",
	"/run",
	"/snap",
}

// destructiveCommands are the first-token commands that warrant path checking.
var destructiveCommands = map[string]bool{
	"rm":       true,
	"rmdir":    true,
	"mv":       true,
	"truncate": true,
	"unlink":   true,
}

// CheckCommand returns an error if cmd is considered dangerous.
//
// Three independent layers:
//  1. Normalize: expand common obfuscation (~ → /home/..., remove quoting artefacts)
//  2. Pattern blacklist: match against hardBlockedPatterns
//  3. Path guard: destructive commands must not target protectedPaths
//
// This is defence-in-depth — bypassing one layer still fails the others.
func CheckCommand(cmd string) error {
	norm := normalize(cmd)

	// Layer 1: hard-blocked patterns on normalized command
	for _, p := range hardBlockedPatterns {
		if p.re.MatchString(norm) {
			return fmt.Errorf("shell/blacklist: blocked — %s", p.reason)
		}
	}

	// Layer 2: path guard for destructive operations
	tokens := strings.Fields(norm)
	if len(tokens) == 0 {
		return nil
	}
	firstCmd := filepath.Base(tokens[0])
	if destructiveCommands[firstCmd] {
		for _, tok := range tokens[1:] {
			// strip flag tokens
			if strings.HasPrefix(tok, "-") {
				continue
			}
			clean := filepath.Clean(tok)
			for _, protected := range protectedPaths {
				if clean == protected || strings.HasPrefix(clean, protected+"/") {
					return fmt.Errorf(
						"shell/blacklist: blocked — %s targets protected path %q",
						firstCmd, protected,
					)
				}
			}
		}
	}

	return nil
}

// normalize reduces common obfuscation techniques before pattern matching.
// It is NOT a full shell parser — it catches the most common bypass attempts.
func normalize(cmd string) string {
	s := cmd

	// Lowercase for case-insensitive matching
	s = strings.ToLower(s)

	// Strip common quoting artefacts that don't change semantics
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\'`, `'`)

	// Expand tilde to /root or /home so path checks work
	s = strings.ReplaceAll(s, "~/", "/home/user/")
	s = strings.ReplaceAll(s, " ~", " /home/user")

	// Collapse multiple spaces / tabs
	spaceRe := regexp.MustCompile(`\s+`)
	s = spaceRe.ReplaceAllString(s, " ")

	return strings.TrimSpace(s)
}
