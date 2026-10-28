package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Raindancer118/nak-api/internal/app"
	"github.com/Raindancer118/nak-api/internal/tools"
)

// fakeSources stands in for the five tools the watcher reads.
type fakeSources struct {
	mu         sync.Mutex
	grade      string
	news       []map[string]any
	convTime   string
	notifs     []map[string]any
	deadlines  []map[string]any
	transcript []map[string]any
	pageExtra  []map[string]any
	exams      []map[string]any
	calls      map[string]int
}

func (f *fakeSources) registry() *tools.Registry {
	r := tools.NewRegistry()
	run := func(name string, out func() any) *tools.Tool {
		return &tools.Tool{Name: name, Kind: tools.Read, Desc: name, Params: []tools.Param{{Name: "days", Type: "integer"}, {Name: "limit", Type: "integer"}, {Name: "unread_only", Type: "boolean"}},
			Run: func(*app.App, tools.Args) (any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.calls[name]++
				return out(), nil
			}}
	}
	r.Add(
		run("cis_grades", func() any {
			return map[string]any{"overview": map[string]any{"modules": append([]map[string]any{
				{"module_nr": "I160", "title": "Datenbanksysteme", "grade": f.grade, "status": "bestanden"},
				{"module_nr": "I151", "title": "Softwaretechnik", "grade": "2,0", "status": "bestanden"},
			}, f.pageExtra...)}}
		}),
		run("moodle_whats_new", func() any { return f.news }),
		run("moodle_conversations", func() any {
			return []map[string]any{{"conversationid": 7, "name": "Prof. Muster", "last_message": "Hallo zusammen", "last_time": f.convTime}}
		}),
		run("moodle_notifications", func() any { return f.notifs }),
		run("nak_deadlines", func() any { return map[string]any{"deadlines": f.deadlines} }),
		run("cis_transcript_grades", func() any { return map[string]any{"modules": f.transcript} }),
		run("cis_list_klausuren", func() any { return f.exams }),
		run("cis_status", func() any { return map[string]any{"studiengang": "Wirtschaftsinformatik (B.Sc.)"} }),
		run("cis_timetable", func() any {
			var evs []map[string]any
			for _, d := range []string{"2026-10-05", "2026-10-12", "2026-11-09", "2026-11-16", "2026-11-23"} {
				evs = append(evs, map[string]any{"start": d + "T09:00:00+02:00", "kind": "V"})
			}
			evs = append(evs, map[string]any{"start": "2026-10-19T09:00:00+02:00", "kind": "K"}) // an exam week is no lecture week
			return map[string]any{"events": evs}
		}),
	)
	return r
}

type watchHarness struct {
	srv   *Server
	f     *fakeSources
	now   *time.Time
	ntfy  []string
	ntfyM sync.Mutex
}

func newWatchHarness(t *testing.T, notify app.NotifySettings) *watchHarness {
	t.Helper()
	f := &fakeSources{grade: "", convTime: "2026-10-02 08:00", calls: map[string]int{}}
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	wh := &watchHarness{f: f, now: &now}
	push := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		wh.ntfyM.Lock()
		wh.ntfy = append(wh.ntfy, r.Header.Get("Title")+" | "+string(b))
		wh.ntfyM.Unlock()
	}))
	t.Cleanup(push.Close)
	a := app.FromEnv()
	a.ConfigDir = t.TempDir()
	a.Zone, _ = time.LoadLocation("Europe/Berlin")
	notify.NtfyURL = strings.Replace(notify.NtfyURL, "PUSH", push.URL, 1)
	a.SaveSettings(app.Settings{Notify: notify})
	wh.srv = New(a, f.registry(), Config{Token: token, Version: "t", Now: func() time.Time { return *wh.now }})
	return wh
}

func (w *watchHarness) events(t *testing.T) []Notification {
	t.Helper()
	return w.srv.watch.list()
}

func TestWatcherBaselineThenChanges(t *testing.T) {
	w := newWatchHarness(t, app.NotifySettings{NtfyURL: "PUSH/naknak"})
	w.srv.watch.runDue()
	if n := len(w.events(t)); n != 0 {
		t.Fatalf("first run must only take a baseline, got %d events", n)
	}
	// a grade appears, a message arrives, Moodle has news, a deadline gets urgent
	w.f.mu.Lock()
	w.f.grade = "1,7"
	w.f.convTime = "2026-10-02 11:00"
	w.f.news = []map[string]any{{"course": "DM2TI25", "courseid": 7566, "cmid": 1, "activity": "probeklausur", "type": "resource", "changes": []string{"1 neue/geänderte Datei(en)"}}}
	w.f.notifs = []map[string]any{{"id": 99, "subject": "Neue Bewertung in Übungsblatt 3", "time": "2026-10-02 11:00", "unread": true}}
	w.f.deadlines = []map[string]any{{"when": "Sa 03.10.2026 23:59", "in_days": 1, "what": "Abgabe Projekt", "kind": "moodle_assign", "source": "moodle", "urgent": true}}
	w.f.mu.Unlock()
	*w.now = w.now.Add(4 * time.Hour) // every check is due again
	w.srv.watch.runDue()
	got := w.events(t)
	titles := ""
	for _, e := range got {
		titles += e.Title + "\n"
	}
	for _, want := range []string{"Neue Note: Datenbanksysteme 1,7", "Neue Nachricht: Prof. Muster", "Neu in DM2TI25: probeklausur", "Neue Bewertung in Übungsblatt 3", "Frist morgen: Abgabe Projekt"} {
		if !strings.Contains(titles, want) {
			t.Errorf("missing %q in\n%s", want, titles)
		}
	}
	// ntfy without details by default: no grade on a public server
	w.ntfyM.Lock()
	pushed := strings.Join(w.ntfy, "\n")
	w.ntfyM.Unlock()
	if len(w.ntfy) != len(got) || strings.Contains(pushed, "1,7") || !strings.Contains(pushed, "Neue Note") {
		t.Fatalf("ntfy pushes (%d for %d events):\n%s", len(w.ntfy), len(got), pushed)
	}
	// nothing new → nothing new
	*w.now = w.now.Add(4 * time.Hour)
	w.srv.watch.runDue()
	if len(w.events(t)) != len(got) {
		t.Fatalf("repeated events: %d → %d", len(got), len(w.events(t)))
	}
}

func TestWatcherKeepsIntervalsAndNightPause(t *testing.T) {
	w := newWatchHarness(t, app.NotifySettings{})
	w.srv.watch.runDue()
	first := w.f.calls["cis_grades"]
	*w.now = w.now.Add(20 * time.Minute)
	w.srv.watch.runDue()
	if w.f.calls["cis_grades"] != first || w.f.calls["moodle_conversations"] != 2 {
		t.Fatalf("intervals: grades %d→%d, conversations %d", first, w.f.calls["cis_grades"], w.f.calls["moodle_conversations"])
	}
	*w.now = time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC) // 03:00 in Berlin
	before := w.f.calls["moodle_conversations"]
	w.srv.watch.runDue()
	if w.f.calls["moodle_conversations"] != before {
		t.Fatal("watcher ran at night")
	}
}

func TestWatcherOff(t *testing.T) {
	w := newWatchHarness(t, app.NotifySettings{Off: true})
	w.srv.watch.runDue()
	if len(w.f.calls) != 0 {
		t.Fatalf("calls while off: %v", w.f.calls)
	}
}

func TestNotificationsAPI(t *testing.T) {
	w := newWatchHarness(t, app.NotifySettings{})
	w.srv.watch.add(Notification{Kind: "grade", Title: "Neue Note: X 1,0", URL: "#/noten"})
	srv := httptest.NewServer(w.srv.Handler())
	t.Cleanup(srv.Close)
	get := func() map[string]any {
		req, _ := http.NewRequest("GET", srv.URL+"/api/notifications", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var m map[string]any
		json.NewDecoder(res.Body).Decode(&m)
		return m
	}
	m := get()
	if m["unread"] != float64(1) {
		t.Fatalf("unread: %v", m)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/notifications/read", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if m := get(); m["unread"] != float64(0) {
		t.Fatalf("after read: %v", m)
	}
}

func TestEventStreamDeliversNotifications(t *testing.T) {
	w := newWatchHarness(t, app.NotifySettings{})
	srv := httptest.NewServer(w.srv.Handler())
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest("GET", srv.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content type %q", res.Header.Get("Content-Type"))
	}
	var got atomic.Value
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		var all string
		for {
			n, err := res.Body.Read(buf)
			all += string(buf[:n])
			if strings.Contains(all, "Neue Note") || err != nil {
				got.Store(all)
				close(done)
				return
			}
		}
	}()
	time.Sleep(50 * time.Millisecond)
	w.srv.watch.add(Notification{Kind: "grade", Title: "Neue Note: Y 2,0"})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("no event on the stream")
	}
	if s, _ := got.Load().(string); !strings.Contains(s, "event: notification") {
		t.Fatalf("stream: %q", s)
	}
}

func TestDemoTickRotatesInventedEvents(t *testing.T) {
	w := newWatchHarness(t, app.NotifySettings{})
	w.srv.watch.demoTick()
	w.srv.watch.demoTick()
	got := w.events(t)
	if len(got) != 2 || got[0].Title == got[1].Title {
		t.Fatalf("want two different demo events, got %+v", got)
	}
	for _, e := range got {
		if !strings.HasPrefix(e.URL, "#/") || e.Kind == "" {
			t.Errorf("demo event must link into the portal: %+v", e)
		}
	}
}

// an exam written this morning: the PDF is read every 10 minutes and its grade
// reported once, even when the Leistungsübersicht later spells it differently
func TestFastGradesWhileAGradeIsPending(t *testing.T) {
	w := newWatchHarness(t, app.NotifySettings{FastGrades: true})
	w.f.exams = []map[string]any{{"exam_id": "77", "module_nr": "I168", "title": "Diskrete Mathematik 2", "start": "02.10.2026 08:30", "registered": true}}
	if _, err := w.srv.cachedTool("cis_list_klausuren", tools.Args{}); err != nil {
		t.Fatal(err)
	}
	w.srv.watch.runDue() // baseline
	if w.f.calls["cis_transcript_grades"] != 1 {
		t.Fatalf("pending grade: PDF read %d times on the first pass", w.f.calls["cis_transcript_grades"])
	}
	*w.now = w.now.Add(10 * time.Minute)
	w.f.mu.Lock()
	w.f.transcript = []map[string]any{{"module_nr": "I168", "title": "Diskrete Mathematik 2", "grade": "2,7 (2. Versuch)"}}
	w.f.mu.Unlock()
	w.srv.watch.runDue()
	got := w.events(t)
	if len(got) != 1 || !strings.Contains(got[0].Title, "Diskrete Mathematik 2 2,7") {
		t.Fatalf("events %+v", got)
	}
	// the page catches up with its own spelling: no second notification
	*w.now = w.now.Add(4 * time.Hour)
	w.f.mu.Lock()
	w.f.grade = "2,0"
	w.f.pageExtra = []map[string]any{{"module_nr": "I168", "title": "Diskrete Mathematik 2", "grade": "2,7 (2.Versuch)", "exam_date": "02.10.2026"}}
	w.f.mu.Unlock()
	w.srv.watch.runDue()
	if n := len(w.events(t)); n != 2 {
		t.Fatalf("want exactly one more (Datenbanksysteme), got %d events: %+v", n, w.events(t))
	}
	// graded now: the fast check stops
	before := w.f.calls["cis_transcript_grades"]
	*w.now = w.now.Add(30 * time.Minute)
	w.srv.watch.runDue()
	if w.f.calls["cis_transcript_grades"] != before {
		t.Error("no pending grade left: the PDF must not be read any more")
	}
}

func TestFastGradesOffOrNothingPending(t *testing.T) {
	off := newWatchHarness(t, app.NotifySettings{})
	off.f.exams = []map[string]any{{"exam_id": "77", "module_nr": "I168", "title": "DM2", "start": "02.10.2026 08:30", "registered": true}}
	off.srv.cachedTool("cis_list_klausuren", tools.Args{})
	off.srv.watch.runDue()
	idle := newWatchHarness(t, app.NotifySettings{FastGrades: true})
	idle.srv.watch.runDue()
	if off.f.calls["cis_transcript_grades"] != 0 || idle.f.calls["cis_transcript_grades"] != 0 {
		t.Errorf("PDF read with the switch off (%d) or nothing pending (%d)", off.f.calls["cis_transcript_grades"], idle.f.calls["cis_transcript_grades"])
	}
}

func TestPendingGradeDueAfterFourLectureWeeks(t *testing.T) {
	w := newWatchHarness(t, app.NotifySettings{})
	w.f.exams = []map[string]any{{"exam_id": "77", "module_nr": "A222,I222", "title": "Diskrete Mathematik 2", "start": "02.10.2026 08:30", "registered": true}}
	w.srv.cachedTool("cis_list_klausuren", tools.Args{})
	rec := httptest.NewRecorder()
	w.srv.pendingAPI(rec, httptest.NewRequest("GET", "/api/grades/pending", nil))
	var out struct {
		Pending []pendingGrade `json:"pending"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Pending) != 1 || out.Pending[0].Due != "22.11.2026" || !out.Pending[0].DueKnown {
		t.Fatalf("pending = %+v (%s)", out.Pending, rec.Body.String())
	}
	// the grade arrives (PDF, under the module number, not the exam number)
	w.f.transcript = []map[string]any{{"module_nr": "I168", "title": "Diskrete Mathematik 2", "grade": "2,7"}}
	*w.now = w.now.Add(time.Hour)
	w.srv.cachedTool("cis_transcript_grades", tools.Args{})
	if n := len(w.srv.pendingExams(*w.now)); n != 0 {
		t.Fatalf("graded exam still pending (%d)", n)
	}
}

// A module whose next attempt is written but not graded shows no grade at all — not the
// 5,0 of the earlier attempt (Tom, 28.10.2026). Once the result is in, it shows again.
func TestPendingAttemptHidesTheEarlierGrade(t *testing.T) {
	w := newWatchHarness(t, app.NotifySettings{})
	w.f.pageExtra = []map[string]any{{"module_nr": "I168", "title": "Diskrete Mathematik 2", "exam_date": "10.07.2026", "grade": "5,0", "grade_value": 5.0, "attempt": 1, "status": "nicht bestanden", "credits": "5"}}
	w.f.exams = []map[string]any{{"exam_id": "77", "module_nr": "A222,I222", "title": "Diskrete Mathematik 2", "start": "02.10.2026 08:30", "registered": true}}
	w.srv.cachedTool("cis_list_klausuren", tools.Args{})
	srv := httptest.NewServer(w.srv.Handler())
	t.Cleanup(srv.Close)
	module := func() map[string]any {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+"/api/tools/cis_grades", strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out struct {
			Result struct {
				Overview struct {
					Modules []map[string]any `json:"modules"`
				} `json:"overview"`
				Stats map[string]any `json:"stats"`
			} `json:"result"`
		}
		json.NewDecoder(res.Body).Decode(&out)
		for _, m := range out.Result.Overview.Modules {
			if m["module_nr"] == "I168" {
				m["failed_total"] = out.Result.Stats["failed"]
				return m
			}
		}
		t.Fatal("I168 missing")
		return nil
	}
	m := module()
	if m["grade"] != "" || m["status"] != "ausstehend" || m["grade_value"] != nil {
		t.Fatalf("pending module shows %v / %v / %v", m["grade"], m["status"], m["grade_value"])
	}
	if m["failed_total"] != 0.0 && m["failed_total"] != nil {
		t.Fatalf("pending attempt still counted as failed: %v", m["failed_total"])
	}
	// the result arrives: shown again
	w.f.transcript = []map[string]any{{"module_nr": "I168", "title": "Diskrete Mathematik 2", "grade": "2,7"}}
	*w.now = w.now.Add(time.Hour)
	w.srv.cachedTool("cis_transcript_grades", tools.Args{})
	if m := module(); m["status"] == "ausstehend" || m["grade"] == "" {
		t.Fatalf("graded module still hidden: %v", m)
	}
}

// An exam naknak never saw (written before it tracked exams) can be entered by hand; from
// then on it is pending like any other, and it goes once the grade is in (Tom, 28.10.2026).
func TestManualExamCountsAsPendingUntilGraded(t *testing.T) {
	w := newWatchHarness(t, app.NotifySettings{})
	w.f.pageExtra = []map[string]any{{"module_nr": "I154", "title": "Allgemeine Volkswirtschaftslehre", "grade": "", "status": "offen", "credits": "5"}}
	srv := httptest.NewServer(w.srv.Handler())
	t.Cleanup(srv.Close)
	call := func(method, path, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return res
	}
	for _, bad := range []string{`{"module_nr":"I151","title":"Softwaretechnik","date":"03.10.2027"}`, `{"module_nr":"","title":"x","date":"30.09.2026"}`, `{"module_nr":"I151","title":"S","date":"30-09-2026"}`} {
		if res := call("POST", "/api/history/exams", bad); res.StatusCode != 400 {
			t.Fatalf("%s accepted: %d", bad, res.StatusCode)
		}
	}
	res := call("POST", "/api/history/exams", `{"module_nr":"I154","title":"Allgemeine Volkswirtschaftslehre","date":"30.09.2026"}`)
	var added struct {
		ID string `json:"exam_id"`
	}
	json.NewDecoder(res.Body).Decode(&added)
	if res.StatusCode != 200 || added.ID == "" {
		t.Fatalf("add: %d %+v", res.StatusCode, added)
	}
	pending := w.srv.pendingExams(*w.now)
	if len(pending) != 1 || pending[0].ModuleNr != "I154" || !pending[0].Manual {
		t.Fatalf("pending = %+v", pending)
	}
	// the module shows "ausstehend"
	raw, _ := w.srv.cachedTool("cis_grades", tools.Args{})
	out := w.srv.withPendingAttempts("cis_grades", tools.Args{}, raw)
	if !strings.Contains(string(out), `"status":"ausstehend"`) {
		t.Fatalf("module not pending: %s", out)
	}
	// only hand-made entries can be removed
	if res := call("DELETE", "/api/history/exams/12407", ""); res.StatusCode != 404 {
		t.Fatalf("CIS exam deletable: %d", res.StatusCode)
	}
	if res := call("DELETE", "/api/history/exams/"+added.ID, ""); res.StatusCode != 200 || len(w.srv.pendingExams(*w.now)) != 0 {
		t.Fatalf("remove: %d", res.StatusCode)
	}
}
