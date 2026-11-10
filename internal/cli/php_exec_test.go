package cli

import (
	"reflect"
	"testing"

	"github.com/geodro/lerd/internal/config"
)

func TestPhpScriptArgIndex(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"bare script", []string{"artisan", "migrate"}, 0},
		{"absolute script", []string{"/tmp/ide-phpinfo.php"}, 0},
		{"glued -d before script", []string{"-dmemory_limit=-1", "/tmp/x.php"}, 1},
		{"separated -d before script", []string{"-d", "memory_limit=-1", "script.php"}, 2},
		{"script then data file arg", []string{"check.php", "/tmp/input.xml"}, 0},
		{"-r runs no file", []string{"-r", "echo 1;"}, -1},
		{"-v runs no file", []string{"-v"}, -1},
		{"-f names the script", []string{"-f", "/tmp/x.php"}, 1},
		{"empty", nil, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := phpScriptArgIndex(tt.args); got != tt.want {
				t.Errorf("phpScriptArgIndex(%v) = %d, want %d", tt.args, got, tt.want)
			}
		})
	}
}

func TestPhpInfoOnly(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"version", []string{"-v"}, true},
		{"long version", []string{"--version"}, true},
		{"modules", []string{"-m"}, true},
		{"phpinfo", []string{"-i"}, true},
		{"ini files", []string{"--ini"}, true},
		{"help", []string{"-h"}, true},
		{"extension info", []string{"--ri", "xdebug"}, true},
		{"extension info with equals", []string{"--ri=json"}, true},
		{"ini flags around info", []string{"-n", "-d", "memory_limit=-1", "-dxdebug.mode=off", "-m"}, true},
		{"lint files", []string{"-l", "src/Foo.php", "src/Bar.php"}, true},
		{"ini flags alone", []string{"-d", "memory_limit=-1"}, false},
		{"script", []string{"artisan", "migrate"}, false},
		{"info flag then script", []string{"-v", "check.php"}, false},
		{"inline code", []string{"-r", "echo 1;"}, false},
		{"built-in server", []string{"-S", "localhost:8000"}, false},
		{"interactive", []string{"-a"}, false},
		{"empty reads stdin", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := phpInfoOnly(tt.args); got != tt.want {
				t.Errorf("phpInfoOnly(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestSpxPassthroughEnv(t *testing.T) {
	tests := []struct {
		name    string
		environ []string
		want    []string
	}{
		{
			name:    "no SPX vars yields nothing",
			environ: []string{"HOME=/home/x", "PATH=/bin"},
			want:    nil,
		},
		{
			name:    "enabled with no report defaults to full",
			environ: []string{"PATH=/bin", "SPX_ENABLED=1"},
			want:    []string{"SPX_ENABLED=1", "SPX_REPORT=full"},
		},
		{
			name:    "explicit report is left untouched",
			environ: []string{"SPX_ENABLED=1", "SPX_REPORT=fp"},
			want:    []string{"SPX_ENABLED=1", "SPX_REPORT=fp"},
		},
		{
			name:    "disabled SPX never gets a default report",
			environ: []string{"SPX_ENABLED=0"},
			want:    []string{"SPX_ENABLED=0"},
		},
		{
			name:    "other SPX vars are forwarded as-is",
			environ: []string{"SPX_SAMPLING_PERIOD=100", "SPX_ENABLED=1"},
			want:    []string{"SPX_SAMPLING_PERIOD=100", "SPX_ENABLED=1", "SPX_REPORT=full"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := spxPassthroughEnv(tt.environ)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("spxPassthroughEnv(%v) = %v, want %v", tt.environ, got, tt.want)
			}
		})
	}
}

// A CLI exec must tag the container with the registered site name so debug
// events (queries included) resolve to a site rather than falling back to the
// working-directory basename, or to nothing at all (#1005).
func TestDebugSiteEnvArgs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	if err := config.AddSite(config.Site{
		Name:    "rapids",
		Domains: []string{"harborlist.test"},
		Path:    dir,
	}); err != nil {
		t.Fatal(err)
	}
	got := debugSiteEnvArgs(dir)
	want := []string{"--env", "LERD_SITE=rapids"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("args = %v, want %v", got, want)
	}
	if got := debugSiteEnvArgs(t.TempDir()); got != nil {
		t.Errorf("unregistered dir args = %v, want none", got)
	}
}

func TestXdebugPassthroughEnv(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin",
		"XDEBUG_MODE=debug",
		"XDEBUG_CONFIG=idekey=PHPSTORM",
		"XDEBUG_SESSION=1",
		"PHP_IDE_CONFIG=serverName=app.test",
		"XDEBUG_UNRELATED_HOST_VAR=x",
		"XDEBUG_TRIGGER=",
	}
	want := []string{"XDEBUG_MODE=debug", "XDEBUG_CONFIG=idekey=PHPSTORM", "XDEBUG_SESSION=1", "PHP_IDE_CONFIG=serverName=app.test"}
	if got := xdebugPassthroughEnv(environ); !reflect.DeepEqual(got, want) {
		t.Errorf("xdebugPassthroughEnv = %v, want %v", got, want)
	}
	if got := xdebugPassthroughEnv([]string{"PATH=/usr/bin"}); len(got) != 0 {
		t.Errorf("no debug vars should forward nothing, got %v", got)
	}
}
