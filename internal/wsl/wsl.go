// Package wsl holds the WSL2-specific detection and config-patching logic that
// `lerd wsl:setup` and the doctor's WSL checks share. The file-patch helpers are
// pure (content in, content out) so they're unit-testable without touching real
// /etc/wsl.conf, ~/.config/containers/containers.conf, or %USERPROFILE%\.wslconfig.
package wsl

import (
	"encoding/base64"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"
)

// IsWSL reports whether we're running inside a WSL2 distro. WSL sets
// WSL_DISTRO_NAME in every shell, and the kernel release carries "microsoft"
// (WSL2) or "WSL"; either is conclusive.
func IsWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		s := strings.ToLower(string(b))
		if strings.Contains(s, "microsoft") || strings.Contains(s, "wsl") {
			return true
		}
	}
	return false
}

// SystemdBooted is sd_booted(3): systemd is PID 1 when this directory exists.
// WSL distros start without it unless /etc/wsl.conf turns it on.
func SystemdBooted() bool {
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

// EnsureSectionLine guarantees an ini/toml file's `[section]` contains a line
// assigning `key`, setting it to `line` (the full `key=value` text, so callers
// control quoting and spacing). It returns the updated content and whether
// anything changed: a key already set to `line` is left untouched (idempotent),
// a key set to something else is rewritten, a missing key is inserted under an
// existing section, and a missing section is appended. Good enough for the flat,
// hand-edited config files WSL setup touches; not a general TOML parser.
func EnsureSectionLine(content, section, key, line string) (string, bool) {
	keyRe := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*=`)
	sectionHeader := "[" + section + "]"

	lines := strings.Split(content, "\n")
	inSection := false
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if trimmed == sectionHeader {
				inSection = true
			} else if inSection {
				// Left our section without finding the key — insert at its end.
				return insertAt(lines, lastContent(lines, i), line), true
			} else {
				inSection = false
			}
			continue
		}
		if inSection && keyRe.MatchString(l) {
			if strings.TrimSpace(l) == strings.TrimSpace(line) {
				return content, false // already set as desired
			}
			lines[i] = line
			return strings.Join(lines, "\n"), true
		}
	}

	if inSection {
		// Section was the last block and the key was absent — append the line.
		return insertAt(lines, lastContent(lines, len(lines)), line), true
	}

	// Section absent entirely — append it.
	prefix := content
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	return prefix + sectionHeader + "\n" + line + "\n", true
}

// lastContent steps back from i over blank lines, so a key added to the end of
// a section sits under its last key instead of after the separating blank line.
func lastContent(lines []string, i int) int {
	for i > 0 && strings.TrimSpace(lines[i-1]) == "" {
		i--
	}
	return i
}

// insertAt splices line into lines just before index i, returning the joined text.
func insertAt(lines []string, i int, line string) string {
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:i]...)
	out = append(out, line)
	out = append(out, lines[i:]...)
	return strings.Join(out, "\n")
}

// WSLConfigLines are lerd's .wslconfig settings: mirrored networking so Windows
// reaches the sites, also by its own LAN IP as a LAN share prints it, and no idle
// timeout, which would stop every container once the last terminal closes.
var WSLConfigLines = []struct{ Section, Key, Line string }{
	{"wsl2", "networkingMode", "networkingMode=mirrored"},
	{"wsl2", "dnsTunneling", "dnsTunneling=true"},
	{"wsl2", "firewall", "firewall=true"},
	{"wsl2", "autoProxy", "autoProxy=true"},
	{"general", "instanceIdleTimeout", "instanceIdleTimeout=-1"},
	{"experimental", "hostAddressLoopback", "hostAddressLoopback=true"},
}

// WSLConfigMissing lists the WSLConfigLines keys a .wslconfig does not yet set
// to lerd's value, in WSLConfigLines order.
func WSLConfigMissing(content string) []string {
	var out []string
	for _, kv := range WSLConfigLines {
		if _, changed := EnsureSectionLine(content, kv.Section, kv.Key, kv.Line); changed {
			out = append(out, kv.Key)
		}
	}
	return out
}

// MirroredNetworkingMinBuild is the first Windows build, 11 22H2, whose WSL
// offers networkingMode=mirrored.
const MirroredNetworkingMinBuild = 22621

// HasEventsLoggerJournald reports whether a containers.conf already sets
// events_logger to journald, used by the doctor to flag the misconfiguration
// that breaks every `podman logs --follow` (and thus every dashboard log pane)
// on a systemd WSL host.
func HasEventsLoggerJournald(content string) bool {
	re := regexp.MustCompile(`(?m)^\s*events_logger\s*=\s*"?journald"?`)
	return re.MatchString(content)
}

// generatedMarker opens every resolv.conf WSL writes itself.
const generatedMarker = "automatically generated by WSL"

// GeneratedNameservers returns the nameservers and search suffixes of a
// resolv.conf WSL generated, which point past systemd-resolved at WSL's proxy,
// or nil for anyone else's.
func GeneratedNameservers(resolvConf string) (nameservers, search []string) {
	if !strings.Contains(resolvConf, generatedMarker) {
		return nil, nil
	}
	for _, l := range strings.Split(resolvConf, "\n") {
		f := strings.Fields(l)
		switch {
		case len(f) == 2 && f[0] == "nameserver":
			nameservers = append(nameservers, f[1])
		case len(f) > 1 && f[0] == "search":
			search = append(search, f[1:]...)
		}
	}
	return nameservers, search
}

// ResolvedUpstreamDropin renders the systemd-resolved drop-in that keeps WSL's
// DNS proxy as the upstream, and its search suffixes, once resolved owns resolv.conf.
func ResolvedUpstreamDropin(nameservers, search []string) string {
	out := "[Resolve]\nDNS=" + strings.Join(nameservers, " ") + "\n"
	if len(search) > 0 {
		out += "Domains=" + strings.Join(search, " ") + "\n"
	}
	return out
}

// WindowsTool resolves a Windows program such as reg.exe by its full path, which
// interop still runs from a systemd service whose PATH has no Windows folders;
// wslpath gives the path, honouring a custom automount root.
func WindowsTool(name string) string {
	return windowsFile(`C:\Windows\System32\`+name, name)
}

// MissingControllers lists the controllers in want that a cgroup.controllers
// file does not delegate. Without pids, crun refuses every container
// (podman#29749, seen on some WSL kernels).
func MissingControllers(controllers string, want ...string) []string {
	have := strings.Fields(controllers)
	var missing []string
	for _, c := range want {
		if !slices.Contains(have, c) {
			missing = append(missing, c)
		}
	}
	return missing
}

// KernelTooOldForNetavark reports a kernel before 6.18 under netavark 2+, whose
// nftables rules need NFT_FIB_INET, which older WSL kernels lack, so no container
// gets a network. netavarkVersion reads like "netavark 2.1.0".
func KernelTooOldForNetavark(kernelRelease, netavarkVersion string) bool {
	// major reads "6.18.40.1-microsoft…" as 6 and 18.
	major := func(s string) (int, int, bool) {
		parts := strings.SplitN(s, ".", 3)
		if len(parts) < 2 {
			return 0, 0, false
		}
		num := func(p string) (int, error) {
			return strconv.Atoi(p[:len(p)-len(strings.TrimLeftFunc(p, unicode.IsDigit))])
		}
		a, errA := num(parts[0])
		b, errB := num(parts[1])
		return a, b, errA == nil && errB == nil
	}
	nv, _, ok := major(strings.TrimPrefix(netavarkVersion, "netavark "))
	if !ok || nv < 2 {
		return false
	}
	km, kn, ok := major(kernelRelease)
	return ok && (km < 6 || km == 6 && kn < 18)
}

// isWindowsDrivePath reports whether p is on a drive WSL mounts from Windows,
// /mnt/<letter>, as opposed to /mnt/wsl and /mnt/wslg, which are WSL's own.
func isWindowsDrivePath(p string) bool {
	rest, ok := strings.CutPrefix(p, "/mnt/")
	if !ok || rest == "" {
		return false
	}
	drive, _, _ := strings.Cut(rest, "/")
	return len(drive) == 1 && unicode.IsLetter(rune(drive[0]))
}

// OnWindowsDrive reports whether p is a Windows drive seen from WSL. Those are
// served over 9p (drvfs): every file access is a round trip to Windows, and
// inotify sees no change made there.
func OnWindowsDrive(p string) bool {
	return isWindowsDrivePath(p) && IsWSL()
}

// EncodePowerShell renders script for -EncodedCommand (base64 of UTF-16LE),
// which carries it through WSL interop and Windows argument parsing untouched.
func EncodePowerShell(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, r := range u {
		b[2*i], b[2*i+1] = byte(r), byte(r>>8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// PowerShell is the path of Windows PowerShell, or "" outside WSL or when its
// file is not there. Resolved once: notifications ask on every delivery.
var PowerShell = sync.OnceValue(func() string {
	if !IsWSL() {
		return ""
	}
	p := windowsFile(`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, "powershell.exe")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
})

// windowsFile resolves name on PATH, else the Windows file winPath through
// wslpath, falling back to the bare name.
func windowsFile(winPath, name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	out, err := exec.Command("wslpath", "-u", winPath).Output()
	if err != nil {
		return name
	}
	return strings.TrimSpace(string(out))
}

var generateResolvConfOff = regexp.MustCompile(`(?im)^\s*generateResolvConf\s*=\s*false\s*$`)

// ResolvConfAwaitsRestart reports resolv.conf, given as its symlink target, still
// on WSL's proxy after wsl.conf handed it to resolved: wsl.conf is only read when
// the distro starts, so it waits for a `wsl --shutdown`.
func ResolvConfAwaitsRestart(wslConf, resolvTarget string) bool {
	return generateResolvConfOff.MatchString(wslConf) && !strings.HasSuffix(resolvTarget, "/run/systemd/resolve/stub-resolv.conf")
}
