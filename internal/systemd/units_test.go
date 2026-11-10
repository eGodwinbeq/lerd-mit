package systemd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PR #255 switches lerd-ui and lerd-watcher to Type=notify so systemctl start
// blocks until the process actually finishes setup (UI listener accepting,
// watcher goroutines live). The two unit files in this package's units/
// directory are the source of truth shipped by the installer; assert each
// one declares the type so the change can't silently regress.
func TestServiceUnitsDeclareTypeNotify(t *testing.T) {
	cases := []struct {
		file string
	}{
		{"lerd-ui.service"},
		{"lerd-watcher.service"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("units", tc.file))
			if err != nil {
				t.Fatalf("read %s: %v", tc.file, err)
			}
			if !containsLine(string(data), "Type=notify") {
				t.Errorf("%s: missing Type=notify directive\n--- file ---\n%s", tc.file, data)
			}
		})
	}
}

func containsLine(content, line string) bool {
	for _, l := range strings.Split(content, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

// lerd-ui binds port 7073, which under WSL's mirrored networking Windows can
// still hold for a few seconds after a restart. Its restarts must be spaced so
// systemd's default limit, 5 starts in 10s, never gives up on the dashboard.
func TestUIUnitKeepsRetryingABusyPort(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("units", "lerd-ui.service"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsLine(string(data), "RestartSec=3") {
		t.Errorf("lerd-ui.service: want RestartSec=3, so five starts span more than the 10s limit\n--- file ---\n%s", data)
	}
}
