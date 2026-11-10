// Package winshim is the platform-neutral logic behind lerd.exe, the command
// shim and the agent's DNS answers, kept apart from cmd/lerd-windows so it is
// tested on Linux like the rest.
package winshim

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
)

// ConfigName is the file lerd.exe reads from its own directory. wsl:setup
// writes it, since lerd.exe cannot ask the distro without knowing which one.
const ConfigName = "lerd-wsl.conf"

// Config tells lerd.exe which distro runs lerd and where the binary is.
type Config struct {
	Distro string // WSL distro name, as `wsl -l` prints it
	Lerd   string // absolute path of the lerd binary inside the distro
	TLD    string // site TLD the agent answers for; empty when DNS is off
	Shims  string // lerd's shim directory inside the distro; empty before wsl:setup wrote it
	Mount  string // where WSL mounts Windows drives, /mnt unless wsl.conf moves it
	User   string // the Linux user who ran wsl:setup; empty in a config from before it was saved
}

func (c Config) String() string {
	return fmt.Sprintf("distro=%s\nlerd=%s\ntld=%s\nshims=%s\nmount=%s\nuser=%s\n", c.Distro, c.Lerd, c.TLD, c.Shims, c.Mount, c.User)
}

// DebugEnv are the variables an IDE sets to debug or profile a PHP run. lerd
// forwards them into the PHP container, and lerd.exe across WSL with WSLENV.
var DebugEnv = []string{"XDEBUG_MODE", "XDEBUG_CONFIG", "XDEBUG_SESSION", "XDEBUG_TRIGGER", "PHP_IDE_CONFIG"}

// WSLEnv is WSLENV with every DebugEnv name that set reports as set added, so
// wsl.exe carries them into the distro. Names already listed stay as they are.
func WSLEnv(current string, set func(string) bool) string {
	listed := map[string]bool{}
	for _, e := range strings.Split(current, ":") {
		name, _, _ := strings.Cut(e, "/")
		listed[name] = true
	}
	out := current
	for _, name := range DebugEnv {
		if listed[name] || !set(name) {
			continue
		}
		if out != "" {
			out += ":"
		}
		out += name
	}
	return out
}

// ParseConfig reads the key=value file wsl:setup writes. An unknown key is
// ignored so a newer lerd can add one without breaking an older lerd.exe.
func ParseConfig(s string) (Config, error) {
	var c Config
	for _, l := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		switch k {
		case "distro":
			c.Distro = v
		case "lerd":
			c.Lerd = v
		case "tld":
			c.TLD = v
		case "shims":
			c.Shims = v
		case "mount":
			c.Mount = v
		case "user":
			c.User = v
		}
	}
	if c.Distro == "" || c.Lerd == "" {
		return Config{}, fmt.Errorf("%s needs distro= and lerd=, run `lerd wsl:setup` inside WSL again", ConfigName)
	}
	return c, nil
}

// LinuxPath turns a \\wsl.localhost\<distro>\... (or \\wsl$\...) path into the
// path inside that distro. ok is false for any other path.
func LinuxPath(winPath, distro string) (string, bool) {
	for _, host := range []string{`\\wsl.localhost\`, `\\wsl$\`} {
		prefix := host + distro
		if len(winPath) < len(prefix) || !strings.EqualFold(winPath[:len(prefix)], prefix) {
			continue
		}
		rest := winPath[len(prefix):]
		if rest != "" && rest[0] != '\\' {
			continue // a different distro whose name starts the same
		}
		return "/" + strings.TrimLeft(strings.ReplaceAll(rest, `\`, "/"), "/"), true
	}
	return "", false
}

// UNCPath is the \\wsl.localhost path Windows reaches a distro's path by.
func UNCPath(distro, linuxPath string) string {
	return `\\wsl.localhost\` + distro + strings.ReplaceAll(linuxPath, "/", `\`)
}

// WSLArgs builds wsl.exe's arguments to run lerd in cwd. A folder inside the
// distro is translated here rather than trusting wsl.exe to map a UNC path; a
// drive path is left for wsl.exe, which maps C:\ to /mnt/c itself.
func WSLArgs(c Config, cwd string, args []string) []string {
	return wslArgs(c, cwd, c.Lerd, args)
}

// ShimFromExe names the shim a copy of lerd.exe stands for, php for php.exe,
// or "" for lerd.exe itself and anything else named lerd.
func ShimFromExe(exe string) string {
	base := exe[strings.LastIndexAny(exe, `\/`)+1:]
	name := strings.TrimSuffix(strings.ToLower(base), ".exe")
	if strings.HasPrefix(name, "lerd") {
		return ""
	}
	return name
}

// ShimArgs builds wsl.exe's arguments to run one of lerd's shims, such as php
// or composer, from cwd: what php.cmd and its siblings on the Windows PATH run.
func ShimArgs(c Config, cwd, name string, args []string) ([]string, error) {
	if !shimName.MatchString(name) {
		return nil, fmt.Errorf("%q is not a lerd shim name", name)
	}
	if c.Shims == "" {
		return nil, fmt.Errorf("%s does not name lerd's shim directory, run `lerd wsl:setup` inside WSL again", ConfigName)
	}
	return wslArgs(c, cwd, path.Join(c.Shims, name), args), nil
}

// DriveShare names the share a drive letter such as "Z:" is mapped to, "" for
// a local drive. cmd's pushd maps one to enter a \\wsl.localhost folder,
// which cmd cannot sit in; lerd.exe sets it from the Windows API.
var DriveShare = func(drive string) string { return "" }

// unmapDrive turns a path on a mapped drive back into the share path, which
// wsl.exe can translate and the mapped drive it cannot.
func unmapDrive(p string) string {
	m := drivePath.FindStringSubmatch(p)
	if m == nil {
		return p
	}
	share := DriveShare(strings.ToUpper(m[1]) + ":")
	if share == "" {
		return p
	}
	return strings.TrimRight(share, `\`) + `\` + p[3:]
}

func wslArgs(c Config, cwd, exe string, args []string) []string {
	cwd = unmapDrive(cwd)
	if p, ok := LinuxPath(cwd, c.Distro); ok {
		cwd = p
	}
	out := []string{"-d", c.Distro}
	// The distro's default user need not be the one whose lerd this is.
	if c.User != "" {
		out = append(out, "-u", c.User)
	}
	out = append(out, "--cd", cwd, "--exec", exe)
	for _, a := range args {
		out = append(out, ArgToLinux(c, a))
	}
	return out
}

var (
	drivePath = regexp.MustCompile(`^([A-Za-z]):[\\/]`)
	shimName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// Exists reports whether a path is there from lerd.exe's working folder. A
// relative argument is only taken for a path when it is, since App\Models\User
// and tests\Unit look alike.
var Exists = func(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ArgToLinux turns a Windows path argument, or the path in a --flag=path, into
// the one lerd sees, so a file an IDE passes reaches it; anything else, a PHP
// namespace included, is left as it is.
func ArgToLinux(c Config, arg string) string {
	if flag, val, ok := strings.Cut(arg, "="); ok && strings.HasPrefix(flag, "-") {
		return flag + "=" + ArgToLinux(c, val)
	}
	arg = unmapDrive(arg)
	if p, ok := LinuxPath(arg, c.Distro); ok {
		return p
	}
	m := drivePath.FindStringSubmatch(arg)
	if m == nil {
		if strings.Contains(arg, `\`) && !strings.HasPrefix(arg, `\`) && Exists(arg) {
			return strings.ReplaceAll(arg, `\`, "/")
		}
		return arg
	}
	mount := c.Mount
	if mount == "" {
		mount = "/mnt"
	}
	return path.Join(mount, strings.ToLower(m[1]), strings.ReplaceAll(arg[3:], `\`, "/"))
}
