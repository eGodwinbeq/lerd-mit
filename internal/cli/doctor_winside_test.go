package cli

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseWindowsProbe(t *testing.T) {
	out := "build=22631\r\nagent=yes\r\nca=no\r\nnrpt=yes\r\nlerddir=C:\\Users\\me\\AppData\\Local\\lerd\\bin\r\n" +
		"profile=C:\\Users\\me\r\nhit=php\tmachine\tC:\\xampp\\php\r\nhit=composer\tuser\tC:\\Users\\me\\AppData\\Local\\lerd\\bin\r\nhit=node\t\t\r\nnoise\r\n"
	p := parseWindowsProbe(out)
	if p.build() != 22631 {
		t.Errorf("build = %d", p.build())
	}
	if !p.is("agent", "yes") || !p.is("ca", "no") || !p.is("nrpt", "yes") {
		t.Errorf("values = %v", p.values)
	}
	if p.values["lerddir"] != `C:\Users\me\AppData\Local\lerd\bin` || p.values["profile"] != `C:\Users\me` {
		t.Errorf("paths = %v", p.values)
	}
	want := []pathHit{
		{Name: "php", Scope: "machine", Dir: `C:\xampp\php`},
		{Name: "composer", Scope: "user", Dir: `C:\Users\me\AppData\Local\lerd\bin`},
		{Name: "node"},
	}
	if len(p.hits) != len(want) {
		t.Fatalf("hits = %+v", p.hits)
	}
	for i := range want {
		if p.hits[i] != want[i] {
			t.Errorf("hit %d = %+v, want %+v", i, p.hits[i], want[i])
		}
	}
}

func TestParseWindowsProbeUnknownBuild(t *testing.T) {
	if b := parseWindowsProbe("agent=no\n").build(); b != 0 {
		t.Errorf("missing build should read 0, got %d", b)
	}
}

func TestPathShadows(t *testing.T) {
	lerd := `C:\Users\me\AppData\Local\lerd\bin`
	hits := []pathHit{
		{Name: "php", Scope: "machine", Dir: `C:\xampp\php`},
		{Name: "composer", Scope: "user", Dir: `c:\users\me\appdata\local\lerd\bin\`},
		{Name: "node", Scope: "user", Dir: `C:\nvm4w\nodejs`},
		{Name: "npm"},
	}
	got := pathShadows(hits, lerd)
	if len(got) != 3 || got[0].Name != "php" || got[1].Name != "node" || got[2].Name != "npm" {
		t.Fatalf("shadows = %+v", got)
	}
	if !shadowsNeedAdmin(got) {
		t.Error("a machine PATH entry can only be moved by an administrator")
	}
	if shadowsNeedAdmin(got[1:]) {
		t.Error("user PATH entries and a missing entry need no administrator")
	}
}

func TestCAThumbprint(t *testing.T) {
	der := []byte("not a real certificate, the thumbprint only hashes the DER bytes")
	path := filepath.Join(t.TempDir(), "rootCA.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum(der)
	want := strings.ToUpper(hex.EncodeToString(sum[:]))
	got, err := caThumbprint(path)
	if err != nil || got != want {
		t.Errorf("caThumbprint = %q, %v; want %q", got, err, want)
	}
	if err := os.WriteFile(path, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := caThumbprint(path); err == nil {
		t.Error("a file without a PEM block should error")
	}
}

func TestWindowsProbeScript(t *testing.T) {
	s := windowsProbeScript("test", "ABC123")
	for _, want := range []string{"'build='", "--agent", `Cert:\CurrentUser\Root\ABC123`, "'.test'", "'php'", "'composer'", "lerddir="} {
		if !strings.Contains(s, want) {
			t.Errorf("probe script lacks %q:\n%s", want, s)
		}
	}
	bare := windowsProbeScript("", "")
	if strings.Contains(bare, "Cert:") || strings.Contains(bare, "NrptRule") {
		t.Errorf("no tld and no CA should skip those checks:\n%s", bare)
	}
}

func TestWindowsProbeScriptQuotesTLD(t *testing.T) {
	if s := windowsProbeScript("te'st", ""); !strings.Contains(s, "'.te''st'") {
		t.Errorf("tld not PowerShell-quoted:\n%s", s)
	}
}

func TestPrependToUserPathScript(t *testing.T) {
	s := prependToUserPathScript(`C:\Users\O'Brien\AppData\Local\lerd\bin`)
	if !strings.Contains(s, `'C:\Users\O''Brien\AppData\Local\lerd\bin'`) {
		t.Errorf("folder not quoted:\n%s", s)
	}
	if !strings.Contains(s, "'User'") || strings.Contains(s, "'Machine'") {
		t.Errorf("must only touch the user PATH:\n%s", s)
	}
}

func TestWindowsSideStale(t *testing.T) {
	linux := t.TempDir()
	win := t.TempDir()
	exe := filepath.Join(linux, "lerd.exe")
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(exe, "v2")
	write(filepath.Join(win, "lerd.exe"), "v2")
	write(filepath.Join(win, "php.exe"), "v2")
	if windowsSideStale(exe, win, []string{"php"}) {
		t.Error("matching lerd.exe and every twin present is not stale")
	}
	if !windowsSideStale(exe, win, []string{"php", "composer"}) {
		t.Error("a missing composer.exe twin is stale")
	}
	write(filepath.Join(win, "lerd.exe"), "v1")
	if !windowsSideStale(exe, win, []string{"php"}) {
		t.Error("an older lerd.exe on Windows is stale")
	}
	if windowsSideStale(filepath.Join(linux, "missing.exe"), win, nil) {
		t.Error("without a lerd.exe beside lerd there is nothing to refresh from")
	}
}
