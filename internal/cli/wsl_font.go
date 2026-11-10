package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/geodro/lerd/internal/wsl"
)

// distroTerminalFont is the font the distro's Windows Terminal profile asks for
// and the distro's own files for it. Ubuntu's names Ubuntu Sans Mono, which
// Windows lacks, so the terminal warns each time it opens the distro.
func distroTerminalFont() (face string, files []string) {
	conf, err := os.ReadFile("/etc/wsl-distribution.conf")
	if err != nil {
		return "", nil
	}
	tmpl := wsl.TerminalProfileTemplate(string(conf))
	if tmpl == "" {
		return "", nil
	}
	profile, err := os.ReadFile(tmpl)
	if err != nil {
		return "", nil
	}
	face = wsl.TerminalFontFace(profile)
	if face == "" {
		return "", nil
	}
	_ = filepath.WalkDir("/usr/share/fonts", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && wsl.FontFileFor(face, d.Name()) {
			files = append(files, p)
		}
		return nil
	})
	return face, files
}

// fontInstalledExpr is a PowerShell test for face being registered as a font,
// for this user or the whole machine.
func fontInstalledExpr(face string) string {
	return fmt.Sprintf(`(@('HKCU:\Software\Microsoft\Windows NT\CurrentVersion\Fonts', 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts') | ForEach-Object { (Get-ItemProperty -Path $_ -ErrorAction SilentlyContinue).PSObject.Properties.Name } | Where-Object { $_ -like %s })`,
		psQuote(strings.NewReplacer("[", "`[", "]", "`]", "*", "`*", "?", "`?").Replace(face)+"*"))
}

// fontRegistryName is the name Windows lists a font file under: the face and
// the file's style, without the [wght] axis tag a variable font carries.
func fontRegistryName(face, file string) string {
	base := filepath.Base(file)
	ext := strings.ToLower(filepath.Ext(base))
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if i := strings.IndexByte(stem, '['); i >= 0 {
		stem = stem[:i]
	}
	style := strings.Trim(stem[min(len(stem), len(strings.ReplaceAll(face, " ", ""))):], "-_ ")
	name := face
	if style != "" {
		name += " " + style
	}
	if ext == ".otf" {
		return name + " (OpenType)"
	}
	return name + " (TrueType)"
}

// registerFontsScript registers files, already copied into the user's font
// folder, as fonts for this Windows user, which needs no administrator. The
// registry is only read at sign-in, so each is also loaded into this session.
func registerFontsScript(face string, files []string) string {
	var b strings.Builder
	b.WriteString(`$k = 'HKCU:\Software\Microsoft\Windows NT\CurrentVersion\Fonts'
if (-not (Test-Path $k)) { New-Item -Path $k -Force | Out-Null }
$d = Join-Path $env:LOCALAPPDATA 'Microsoft\Windows\Fonts'
Add-Type -Namespace LerdFont -Name Native -MemberDefinition '[DllImport("gdi32.dll", CharSet = CharSet.Unicode)] public static extern int AddFontResourceW(string f); [DllImport("user32.dll")] public static extern IntPtr SendMessageTimeoutW(IntPtr h, uint m, IntPtr w, IntPtr l, uint f, uint t, out IntPtr r);'
`)
	for _, f := range files {
		name := psQuote(filepath.Base(f))
		fmt.Fprintf(&b, "New-ItemProperty -Path $k -Name %s -Value (Join-Path $d %s) -PropertyType String -Force | Out-Null\n",
			psQuote(fontRegistryName(face, f)), name)
		fmt.Fprintf(&b, "[void][LerdFont.Native]::AddFontResourceW((Join-Path $d %s))\n", name)
	}
	// WM_FONTCHANGE (0x1D) to every window, so running apps see the new font too.
	b.WriteString("$r = [IntPtr]::Zero\n[void][LerdFont.Native]::SendMessageTimeoutW([IntPtr]0xffff, 0x1D, [IntPtr]::Zero, [IntPtr]::Zero, 2, 1000, [ref]$r)\n")
	return b.String()
}

// installTerminalFont copies the distro's font files into the Windows user's
// font folder and registers them. Windows Terminal picks them up when it next starts.
func installTerminalFont(face string, files []string) error {
	local, err := windowsEnvPath("LOCALAPPDATA")
	if err != nil {
		return err
	}
	dir := filepath.Join(local, "Microsoft", "Windows", "Fonts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		if err := copyFileContents(f, filepath.Join(dir, filepath.Base(f))); err != nil {
			return err
		}
	}
	_, err = powershell(registerFontsScript(face, files))
	return err
}

// terminalFontMissing returns the distro's terminal font when Windows lacks it
// and the distro has the files to install, "" otherwise.
func terminalFontMissing() (face string, files []string) {
	face, files = distroTerminalFont()
	if len(files) == 0 {
		return "", nil
	}
	out, err := powershell("if (" + fontInstalledExpr(face) + ") { 'yes' } else { 'no' }")
	if err != nil || strings.TrimSpace(out) != "no" {
		return "", nil
	}
	return face, files
}
