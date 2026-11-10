package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWindowsElevatedFixesAlwaysConfirm pins that the fixes raising a Windows
// prompt are heavy: `doctor --fix --yes` and the MCP diag tool run unattended,
// and must not put an admin or trust dialog on the desktop by themselves.
func TestWindowsElevatedFixesAlwaysConfirm(t *testing.T) {
	for _, key := range []string{fixWindowsDNS, fixWindowsCA} {
		if !IsHeavyFix(autoFix(key, "test", "x")) {
			t.Errorf("%q raises a Windows prompt but is not heavy", key)
		}
	}
	for _, key := range []string{fixPodmanLogger, fixTrayOff, fixWSLConfig, fixWindowsRefresh, fixWindowsAgent, fixWindowsPath} {
		if IsHeavyFix(autoFix(key, "", "x")) {
			t.Errorf("%q needs no prompt and should run under --yes", key)
		}
	}
}

func TestHeavyFixCarriesConfirmReason(t *testing.T) {
	b, err := json.Marshal(autoFix(fixWindowsDNS, "test", "add the rule"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"confirm":"`) {
		t.Errorf("a heavy fix should tell the UI why it asks first: %s", b)
	}
	b, _ = json.Marshal(autoFix(fixMkdir, "/x", "create it"))
	if strings.Contains(string(b), "confirm") {
		t.Errorf("a light fix carries no confirm reason: %s", b)
	}
}

func TestApplyDoctorFixWindowsDNSNeedsTLD(t *testing.T) {
	var buf strings.Builder
	if err := ApplyDoctorFix(autoFix(fixWindowsDNS, "", "x"), &buf); err == nil {
		t.Error("the DNS rule fix without a TLD should error")
	}
	if err := ApplyDoctorFix(autoFix(fixWindowsPath, "", "x"), &buf); err == nil {
		t.Error("the PATH fix without lerd's folder should error")
	}
}
