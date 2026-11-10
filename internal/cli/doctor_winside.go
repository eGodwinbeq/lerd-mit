package cli

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/geodro/lerd/internal/config"
	"github.com/geodro/lerd/internal/desktopnotify"
	"github.com/geodro/lerd/internal/winshim"
	"github.com/geodro/lerd/internal/wsl"
)

// findingSink is how a check outside runDoctorInto reports into its report.
type findingSink struct {
	ok   func(label string)
	warn func(label, msg string)
	fail func(label, msg, hint string)
	fix  func(*DoctorFix)
}

// windowsProbe is what one PowerShell run reports about the Windows side, so
// the doctor pays for a single interop round trip.
type windowsProbe struct {
	values map[string]string
	hits   []pathHit
}

// pathHit is the folder Windows runs a shim name from: the first on the system
// PATH, then the user PATH, as Windows builds it. Dir is empty when none has it.
type pathHit struct {
	Name, Scope, Dir string
}

func (p windowsProbe) is(key, want string) bool { return p.values[key] == want }

func (p windowsProbe) build() int {
	n, _ := strconv.Atoi(p.values["build"])
	return n
}

// shimsOnWindowsPath are the shims a Windows IDE or terminal reaches by name.
var shimsOnWindowsPath = []string{"php", "composer", "node"}

// windowsProbeScript prints key=value lines and one hit= line per shim lerd.exe
// has a twin for. The CA and DNS rule checks are left out without a CA or TLD.
func windowsProbeScript(tld, caThumb string) string {
	var b strings.Builder
	b.WriteString(`'build=' + [Environment]::OSVersion.Version.Build
try { if (Get-CimInstance Win32_Process -Filter "name='lerd.exe'" | Where-Object { $_.CommandLine -like '*--agent*' }) { 'agent=yes' } else { 'agent=no' } } catch {}
'profile=' + $env:USERPROFILE
$lerd = Join-Path $env:LOCALAPPDATA 'lerd\bin'
'lerddir=' + $lerd
`)
	if caThumb != "" {
		fmt.Fprintf(&b, "if ((Test-Path 'Cert:\\CurrentUser\\Root\\%[1]s') -or (Test-Path 'Cert:\\LocalMachine\\Root\\%[1]s')) { 'ca=yes' } else { 'ca=no' }\n", caThumb)
	}
	if tld != "" {
		fmt.Fprintf(&b, "try { if (Get-DnsClientNrptRule | Where-Object { $_.Namespace -contains %s -and $_.NameServers -contains '127.0.0.1' }) { 'nrpt=yes' } else { 'nrpt=no' } } catch {}\n", psQuote("."+tld))
	}
	fmt.Fprintf(&b, `$tab = [char]9
$scopes = @(@('machine', [Environment]::GetEnvironmentVariable('Path', 'Machine')), @('user', [Environment]::GetEnvironmentVariable('Path', 'User')))
foreach ($n in %s) {
  if (-not (Test-Path -LiteralPath (Join-Path $lerd ($n + '.exe')))) { continue }
  $hit = $n + $tab + $tab
  :scan foreach ($s in $scopes) {
    foreach ($d in ([string]$s[1] -split ';')) {
      if (-not $d) { continue }
      $d = [Environment]::ExpandEnvironmentVariables($d).TrimEnd('\')
      foreach ($e in @('.exe', '.bat', '.cmd')) {
        if (Test-Path -LiteralPath (Join-Path $d ($n + $e))) { $hit = $n + $tab + $s[0] + $tab + $d; break scan }
      }
    }
  }
  'hit=' + $hit
}
`, psList(shimsOnWindowsPath))
	return b.String()
}

func parseWindowsProbe(out string) windowsProbe {
	p := windowsProbe{values: map[string]string{}}
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimRight(l, "\r"), "=")
		if !ok {
			continue
		}
		if k != "hit" {
			p.values[k] = v
			continue
		}
		parts := strings.SplitN(v, "\t", 3)
		for len(parts) < 3 {
			parts = append(parts, "")
		}
		p.hits = append(p.hits, pathHit{Name: parts[0], Scope: parts[1], Dir: parts[2]})
	}
	return p
}

// pathShadows returns the hits Windows does not run from lerdDir: another PHP
// or Composer first on the PATH, or lerd's folder missing from it.
func pathShadows(hits []pathHit, lerdDir string) []pathHit {
	want := strings.TrimRight(lerdDir, `\`)
	var out []pathHit
	for _, h := range hits {
		if h.Dir == "" || !strings.EqualFold(strings.TrimRight(h.Dir, `\`), want) {
			out = append(out, h)
		}
	}
	return out
}

// shadowsNeedAdmin reports a shadow on the system PATH, which Windows puts
// before the user's and only an administrator can change.
func shadowsNeedAdmin(shadows []pathHit) bool {
	for _, h := range shadows {
		if h.Scope == "machine" {
			return true
		}
	}
	return false
}

// prependToUserPathScript puts winDir first on the user PATH, once.
func prependToUserPathScript(winDir string) string {
	return fmt.Sprintf(`$d = %s
$p = @(([Environment]::GetEnvironmentVariable('Path', 'User') -split ';') | Where-Object { $_ -and $_.TrimEnd('\') -ne $d.TrimEnd('\') })
[Environment]::SetEnvironmentVariable('Path', ((@($d) + $p) -join ';'), 'User')`, psQuote(winDir))
}

// caThumbprint is the SHA-1 thumbprint Windows files a certificate under.
func caThumbprint(pemPath string) (string, error) {
	data, err := os.ReadFile(pemPath)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", fmt.Errorf("%s holds no PEM block", pemPath)
	}
	sum := sha1.Sum(block.Bytes)
	return strings.ToUpper(hex.EncodeToString(sum[:])), nil
}

// windowsSideStale reports a Windows side behind this lerd: its lerd.exe
// differs from the one beside lerd, or a shim has no .exe twin yet.
func windowsSideStale(lerdExe, winBin string, shims []string) bool {
	want, err := os.ReadFile(lerdExe)
	if err != nil {
		return false
	}
	have, err := os.ReadFile(filepath.Join(winBin, "lerd.exe"))
	if err != nil || !bytes.Equal(have, want) {
		return true
	}
	for _, n := range shims {
		if _, err := os.Stat(filepath.Join(winBin, n+".exe")); err != nil {
			return true
		}
	}
	return false
}

// linuxPathOf is the path the distro reaches a Windows path at, "" when wslpath
// cannot tell.
func linuxPathOf(win string) string {
	if win == "" {
		return ""
	}
	out, err := exec.Command("wslpath", "-u", win).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// checkWindowsSide checks what WSL users meet from Windows: networking, the
// lerd.exe install, its agent, .test DNS, HTTPS trust and the PATH.
func checkWindowsSide(s findingSink, tld string) {
	thumb := ""
	if pemPath, ok := mkcertRootCAPEM(); ok {
		thumb, _ = caThumbprint(pemPath)
	}
	out, err := powershell(windowsProbeScript(tld, thumb))
	if err != nil {
		s.warn("Windows interop", "powershell.exe did not answer, so the Windows side was not checked: "+err.Error())
		return
	}
	p := parseWindowsProbe(out)

	if b := p.build(); b > 0 && b < wsl.MirroredNetworkingMinBuild {
		s.warn("mirrored networking available", fmt.Sprintf("Windows build %d predates Windows 11 22H2, so Windows browsers cannot reach sites inside WSL; use a browser installed in WSL or upgrade Windows", b))
	} else if conf := linuxPathOf(p.values["profile"]); conf != "" {
		data, _ := os.ReadFile(filepath.Join(conf, ".wslconfig"))
		if missing := wsl.WSLConfigMissing(string(data)); len(missing) > 0 {
			s.warn(".wslconfig settings", "missing "+strings.Join(missing, ", ")+": without mirrored networking Windows cannot reach the sites, and without instanceIdleTimeout=-1 WSL stops them once the last terminal closes")
			s.fix(autoFix(fixWSLConfig, "", "write lerd's settings into .wslconfig, then run `wsl --shutdown` from Windows"))
		} else {
			s.ok(".wslconfig settings")
		}
	}

	lerdDir := p.values["lerddir"]
	winBin := linuxPathOf(lerdDir)
	if _, err := os.Stat(filepath.Join(winBin, winshim.ConfigName)); winBin == "" || err != nil {
		s.warn("Windows integration", "lerd.exe is not set up on Windows, so lerd, php and composer do not work from Windows terminals and IDEs")
		s.fix(manualFixWith("run `lerd wsl:setup`"))
		return
	}
	if self, err := selfPath(); err == nil {
		shims, _ := winshim.ShimNames(config.BinDir())
		if windowsSideStale(filepath.Join(filepath.Dir(self), "lerd.exe"), winBin, shims) {
			s.warn("lerd.exe and shims up to date", "the Windows copies are behind this lerd, or a shim has no .exe yet")
			s.fix(autoFix(fixWindowsRefresh, "", "copy this lerd's lerd.exe and shim .exe files to Windows"))
		} else {
			s.ok("lerd.exe and shims up to date")
		}
	}

	switch {
	case p.is("agent", "no"):
		s.warn("Windows agent running", "nothing boots WSL at login or answers site lookups in Windows")
		s.fix(autoFix(fixWindowsAgent, "", "start the lerd agent"))
	case p.is("agent", "yes"):
		s.ok("Windows agent running")
	}

	switch {
	case tld != "" && p.is("nrpt", "no"):
		s.fail("."+tld+" routed to lerd in Windows", "Windows has no DNS rule sending ."+tld+" to lerd, so sites do not open in Windows browsers", "apply the fix and approve the administrator prompt")
		s.fix(autoFix(fixWindowsDNS, tld, "add the Windows DNS rule for ."+tld))
	case tld != "" && p.is("nrpt", "yes"):
		s.ok("." + tld + " routed to lerd in Windows")
	}

	switch {
	case p.is("ca", "no"):
		s.warn("lerd's CA trusted by Windows", "Windows browsers show a certificate warning on https sites")
		s.fix(autoFix(fixWindowsCA, "", "import lerd's CA into your Windows trust store"))
	case p.is("ca", "yes"):
		s.ok("lerd's CA trusted by Windows")
	}

	shadows := pathShadows(p.hits, lerdDir)
	if len(shadows) == 0 {
		if len(p.hits) > 0 {
			s.ok("lerd's php and composer first on the Windows PATH")
		}
		return
	}
	var which []string
	for _, h := range shadows {
		if h.Dir == "" {
			which = append(which, h.Name+" is not on the PATH")
		} else {
			which = append(which, h.Name+" runs from "+h.Dir)
		}
	}
	s.warn("lerd's php and composer first on the Windows PATH", strings.Join(which, ", ")+", so Windows tools and IDEs use that instead of lerd")
	if shadowsNeedAdmin(shadows) {
		s.fix(manualFixWith("remove those folders from the system PATH (System Properties, Environment Variables), or uninstall the other PHP"))
	} else {
		s.fix(autoFix(fixWindowsPath, lerdDir, "put lerd's folder first on your Windows PATH"))
	}
}

// applyWindowsSideFix runs the WSL and Windows repairs. None runs sudo; the DNS
// rule and the CA raise their own Windows prompt, which is why they are heavy.
func applyWindowsSideFix(fix *DoctorFix, out io.Writer) error {
	switch fix.Key {
	case fixPodmanLogger:
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		fmt.Fprintln(out, `setting events_logger = "journald" in containers.conf`)
		_, err = patchUserFile(filepath.Join(home, ".config", "containers", "containers.conf"), func(c string) (string, bool) {
			return wsl.EnsureSectionLine(c, "engine", "events_logger", `events_logger = "journald"`)
		})
		return err
	case fixTrayOff:
		fmt.Fprintln(out, "turning off lerd-tray")
		turnOffTrayUnit()
		return nil
	case fixWSLConfig:
		if _, err := patchWSLConfig(); err != nil {
			return err
		}
		fmt.Fprintln(out, "wrote .wslconfig; run `wsl --shutdown` from Windows and reopen WSL for it to take effect")
		return nil
	case fixWindowsRefresh:
		fmt.Fprintln(out, "copying lerd.exe and the shim .exe files to Windows")
		return refreshWindowsSide()
	case fixWindowsAgent:
		return startWindowsAgent(out)
	case fixWindowsPath:
		if fix.Arg == "" {
			return fmt.Errorf("windows-path fix: no folder")
		}
		if _, err := powershell(prependToUserPathScript(fix.Arg)); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s is first on your PATH; open a new terminal or restart your IDE to pick it up\n", fix.Arg)
		return nil
	case fixWindowsDNS:
		if fix.Arg == "" {
			return fmt.Errorf("windows-dns fix: no TLD")
		}
		if err := runAsAdmin(out, "routing ."+fix.Arg+" to lerd in Windows DNS", addNRPTScript(fix.Arg)); err != nil {
			return err
		}
		if err := waitForNRPT(fix.Arg); err != nil {
			return err
		}
		fmt.Fprintf(out, "Windows resolves *.%s through lerd\n", fix.Arg)
		return nil
	case fixWindowsCA:
		fmt.Fprintln(out, "importing lerd's CA, confirm the prompt on the Windows desktop")
		switch trustCAOnWindows() {
		case caTrusted:
			return nil
		case caSkippedNoMkcert:
			return fmt.Errorf("lerd has no CA yet, run `lerd secure` on a site first")
		default:
			return fmt.Errorf("could not reach certutil.exe on Windows")
		}
	}
	return fmt.Errorf("unknown fix %q", fix.Key)
}

// startWindowsAgent starts the agent the way wsl:setup does: through the
// desktop app when it is installed, otherwise from lerd.exe directly.
func startWindowsAgent(out io.Writer) error {
	if desktopnotify.AppInstalled() {
		fmt.Fprintln(out, "starting the Lerd desktop app, which runs the agent")
		_, err := powershell(agentHandOverScript())
		return err
	}
	local, err := windowsEnvPath("LOCALAPPDATA")
	if err != nil {
		return err
	}
	winDir, err := windowsPath(filepath.Join(local, "lerd", "bin"))
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "starting the lerd agent")
	_, err = powershell(agentScript(winDir))
	return err
}
