package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/geodro/lerd/internal/cli"
)

func stubDoctor(t *testing.T, rep cli.DoctorReport, apply func(*cli.DoctorFix, io.Writer) error) {
	t.Helper()
	origRun, origApply := runDoctorReport, applyDoctorFix
	runDoctorReport = func() (cli.DoctorReport, error) { return rep, nil }
	applyDoctorFix = apply
	t.Cleanup(func() { runDoctorReport, applyDoctorFix = origRun, origApply })
}

func doctorReportWith(findings ...cli.Finding) cli.DoctorReport {
	return cli.DoctorReport{Version: "test", Findings: findings}
}

func autoFinding(name, key string) cli.Finding {
	return cli.Finding{Section: "WSL2", Name: name, Status: "warn", Fix: &cli.DoctorFix{Tier: cli.FixAuto, Key: key, Arg: "secret-arg", Label: "fix " + name}}
}

func TestHandleDoctorReturnsReportWithoutFixArgs(t *testing.T) {
	stubDoctor(t, doctorReportWith(autoFinding("Windows agent running", "windows-agent")), nil)
	w := httptest.NewRecorder()
	handleDoctor(w, localRequest(http.MethodGet, "/api/doctor", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var rep cli.DoctorReport
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Fix == nil || rep.Findings[0].Fix.Key != "windows-agent" {
		t.Fatalf("report = %+v", rep)
	}
	if strings.Contains(w.Body.String(), "secret-arg") {
		t.Error("a fix's argument stays on the server; the client names a fix, never its target")
	}
}

func TestHandleDoctorRefusesRemote(t *testing.T) {
	stubDoctor(t, doctorReportWith(), nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/doctor", nil)
	r.RemoteAddr = "192.0.2.10:50000"
	handleDoctor(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("remote doctor status = %d, want 403", w.Code)
	}
}

func TestHandleDoctorFixStreamsOutputAndUsesServerArg(t *testing.T) {
	var gotArg string
	stubDoctor(t, doctorReportWith(autoFinding("Windows agent running", "windows-agent")), func(f *cli.DoctorFix, out io.Writer) error {
		gotArg = f.Arg
		fmt.Fprint(out, "starting the lerd agent\npartial")
		return nil
	})
	w := httptest.NewRecorder()
	handleDoctorFix(w, localRequest(http.MethodPost, "/api/doctor/fix", `{"key":"windows-agent","name":"Windows agent running"}`))
	if gotArg != "secret-arg" {
		t.Errorf("fix ran with arg %q, want the one the server found", gotArg)
	}
	events := readFixEvents(t, w.Body.String())
	want := []doctorFixEvent{
		{Phase: "output", Line: "starting the lerd agent"},
		{Phase: "output", Line: "partial"},
		{Phase: "done"},
	}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Errorf("events = %+v, want %+v", events, want)
	}
}

func TestHandleDoctorFixReportsFailure(t *testing.T) {
	stubDoctor(t, doctorReportWith(autoFinding(".test routed", "windows-dns")), func(*cli.DoctorFix, io.Writer) error {
		return fmt.Errorf("the admin prompt was declined")
	})
	w := httptest.NewRecorder()
	handleDoctorFix(w, localRequest(http.MethodPost, "/api/doctor/fix", `{"key":"windows-dns","name":".test routed"}`))
	events := readFixEvents(t, w.Body.String())
	last := events[len(events)-1]
	if last.Phase != "failed" || !strings.Contains(last.Error, "declined") {
		t.Errorf("last event = %+v", last)
	}
}

func TestHandleDoctorFixRejectsStaleOrManualFix(t *testing.T) {
	manual := cli.Finding{Name: "Windows integration", Status: "warn", Fix: &cli.DoctorFix{Tier: cli.FixManual, Label: "run `lerd wsl:setup`"}}
	stubDoctor(t, doctorReportWith(manual), func(*cli.DoctorFix, io.Writer) error {
		t.Fatal("no fix should run")
		return nil
	})
	for _, body := range []string{
		`{"key":"windows-agent","name":"Windows agent running"}`,
		`{"key":"","name":"Windows integration"}`,
		`not json`,
	} {
		w := httptest.NewRecorder()
		handleDoctorFix(w, localRequest(http.MethodPost, "/api/doctor/fix", body))
		if w.Code == http.StatusOK {
			t.Errorf("body %s: status 200, want a refusal", body)
		}
	}
}

func TestHandleDoctorFixRequiresLocalPost(t *testing.T) {
	stubDoctor(t, doctorReportWith(autoFinding("x", "windows-agent")), func(*cli.DoctorFix, io.Writer) error {
		t.Fatal("no fix should run")
		return nil
	})
	w := httptest.NewRecorder()
	handleDoctorFix(w, localRequest(http.MethodGet, "/api/doctor/fix", ""))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d", w.Code)
	}
	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/doctor/fix", strings.NewReader(`{"key":"windows-agent","name":"x"}`))
	r.RemoteAddr = "192.0.2.10:50000"
	handleDoctorFix(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("remote fix status = %d, want 403", w.Code)
	}
}

func readFixEvents(t *testing.T, body string) []doctorFixEvent {
	t.Helper()
	var events []doctorFixEvent
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		var e doctorFixEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("bad line %q: %v", sc.Text(), err)
		}
		events = append(events, e)
	}
	if len(events) == 0 {
		t.Fatal("no events")
	}
	return events
}

func TestHandleDoctorShowsInstallAsManual(t *testing.T) {
	stubDoctor(t, doctorReportWith(autoFinding("lerd services installed", "install")), func(*cli.DoctorFix, io.Writer) error {
		t.Fatal("the install fix restarts the dashboard and must not run from it")
		return nil
	})
	w := httptest.NewRecorder()
	handleDoctor(w, localRequest(http.MethodGet, "/api/doctor", ""))
	var rep cli.DoctorReport
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if fx := rep.Findings[0].Fix; fx == nil || fx.Tier != cli.FixManual || !strings.Contains(fx.Label, "lerd install") {
		t.Errorf("install should be offered as a command to run, got %+v", fx)
	}
	w = httptest.NewRecorder()
	handleDoctorFix(w, localRequest(http.MethodPost, "/api/doctor/fix", `{"key":"install","name":"lerd services installed"}`))
	if w.Code == http.StatusOK {
		t.Error("the install fix must be refused from the dashboard")
	}
}

func TestHandleDoctorFixTellsSameNamesApartByMessage(t *testing.T) {
	a := autoFinding("parked dir: /home/user/projects/cli...", "mkdir")
	a.Message, a.Fix.Arg = "missing /home/user/projects/clients-a", "/home/user/projects/clients-a"
	b := autoFinding("parked dir: /home/user/projects/cli...", "mkdir")
	b.Message, b.Fix.Arg = "missing /home/user/projects/clients-b", "/home/user/projects/clients-b"
	var gotArg string
	stubDoctor(t, doctorReportWith(a, b), func(f *cli.DoctorFix, _ io.Writer) error {
		gotArg = f.Arg
		return nil
	})
	w := httptest.NewRecorder()
	handleDoctorFix(w, localRequest(http.MethodPost, "/api/doctor/fix", `{"key":"mkdir","name":"parked dir: /home/user/projects/cli...","message":"missing /home/user/projects/clients-b"}`))
	if gotArg != "/home/user/projects/clients-b" {
		t.Errorf("fixed %q, want the second finding's target", gotArg)
	}
}

func TestHandleDoctorFixRefusesWhileAnotherRuns(t *testing.T) {
	stubDoctor(t, doctorReportWith(autoFinding("Windows agent running", "windows-agent")), func(*cli.DoctorFix, io.Writer) error {
		t.Fatal("no fix should run while another holds the lock")
		return nil
	})
	fixMu.Lock()
	defer fixMu.Unlock()
	w := httptest.NewRecorder()
	handleDoctorFix(w, localRequest(http.MethodPost, "/api/doctor/fix", `{"key":"windows-agent","name":"Windows agent running"}`))
	if w.Code != http.StatusConflict {
		t.Errorf("status %d, want 409 while another fix runs", w.Code)
	}
}
