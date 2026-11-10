package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/geodro/lerd/internal/cli"
)

// The troubleshooting panel runs the same doctor as `lerd doctor` and applies
// its automatic fixes one at a time. Seams so tests never probe the machine.
var (
	runDoctorReport = cli.RunDoctorReport
	applyDoctorFix  = cli.ApplyDoctorFix
)

// fixMu keeps one fix running at a time, since two repairs racing on one file
// help no one. Diagnosis only reads, so it never waits on a fix.
var fixMu sync.Mutex

func handleDoctor(w http.ResponseWriter, r *http.Request) {
	if !hasHostActionAuthority(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	rep, err := runDoctorReport()
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	for i := range rep.Findings {
		if cli.RestartsDashboard(rep.Findings[i].Fix) {
			rep.Findings[i].Fix = &cli.DoctorFix{Tier: cli.FixManual, Label: "run `lerd install` in a terminal, it restarts the dashboard"}
		}
	}
	writeJSON(w, rep)
}

type doctorFixRequest struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

type doctorFixEvent struct {
	Phase string `json:"phase"` // output, done, failed
	Line  string `json:"line,omitempty"`
	Error string `json:"error,omitempty"`
}

// handleDoctorFix applies one automatic fix the doctor still reports, named by
// key and finding; its target comes from a fresh diagnosis, never the client.
// Local only: a fix can raise a Windows prompt on this machine's desktop.
func handleDoctorFix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isLocalControlRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req doctorFixRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		http.Error(w, "expected {key, name}", http.StatusBadRequest)
		return
	}
	if !fixMu.TryLock() {
		http.Error(w, "another fix is running, wait for it to finish", http.StatusConflict)
		return
	}
	defer fixMu.Unlock()
	rep, err := runDoctorReport()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fix := findAutoFix(rep, req)
	if fix == nil || cli.RestartsDashboard(fix) {
		http.Error(w, "that fix no longer applies, run the checks again", http.StatusConflict)
		return
	}
	writeLine, _ := startNDJSONStream(w, r)
	out := &lineEmitter{emit: func(l string) { writeLine(doctorFixEvent{Phase: "output", Line: l}) }}
	err = applyDoctorFix(fix, out)
	out.flush()
	if err != nil {
		writeLine(doctorFixEvent{Phase: "failed", Error: err.Error()})
		return
	}
	writeLine(doctorFixEvent{Phase: "done"})
}

// findAutoFix matches the message as well as the name, since a name can be cut
// short and repeat, two long parked directories say.
func findAutoFix(rep cli.DoctorReport, req doctorFixRequest) *cli.DoctorFix {
	for _, f := range rep.Findings {
		if f.Fix != nil && f.Fix.Tier == cli.FixAuto && f.Fix.Key == req.Key && f.Name == req.Name && f.Message == req.Message {
			return f.Fix
		}
	}
	return nil
}

// lineEmitter hands each complete line written to it to emit.
type lineEmitter struct {
	emit func(string)
	buf  []byte
}

func (l *lineEmitter) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		l.emit(strings.TrimRight(string(l.buf[:i]), "\r"))
		l.buf = l.buf[i+1:]
	}
}

func (l *lineEmitter) flush() {
	if len(l.buf) > 0 {
		l.emit(strings.TrimRight(string(l.buf), "\r"))
		l.buf = nil
	}
}
