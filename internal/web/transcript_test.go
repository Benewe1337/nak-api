package web

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Raindancer118/nak-api/internal/app"
	"github.com/Raindancer118/nak-api/internal/grades"
	"github.com/Raindancer118/nak-api/internal/tools"
)

// A grade notified from the Notenübersicht PDF must show on the module page
// and in the grades list although the Leistungsübersicht does not have it yet.
func TestTranscriptGradeShowsBeforeThePage(t *testing.T) {
	open := grades.ModuleGrade{ModuleNr: "I166", Title: "OOP", Status: grades.StatusOpen, Credits: "6"}
	page := func() *grades.Overview { return &grades.Overview{Modules: []grades.ModuleGrade{open}} }
	r := tools.NewRegistry()
	r.Add(
		&tools.Tool{Name: "cis_grades", Kind: tools.Read, Params: []tools.Param{{Name: "status"}},
			Run: func(a *app.App, args tools.Args) (any, error) {
				o := page()
				return map[string]any{"overview": o, "stats": o.Stats()}, nil
			}},
		&tools.Tool{Name: "cis_transcript_grades", Kind: tools.Read,
			Run: func(a *app.App, args tools.Args) (any, error) {
				return &grades.TranscriptGrades{Date: "02.10.2026", Modules: []grades.TranscriptGrade{{ModuleNr: "I166", Title: "OOP", Grade: "1,7", Credits: "6"}}}, nil
			}},
		&tools.Tool{Name: "nak_module", Kind: tools.Read, Params: []tools.Param{{Name: "module_nr"}},
			Run: func(a *app.App, args tools.Args) (any, error) {
				g := open
				if args.Str("module_nr") != "I166" {
					g = grades.ModuleGrade{ModuleNr: args.Str("module_nr"), Title: "Automaten", Status: grades.StatusOpen}
				}
				return map[string]any{"module_nr": args.Str("module_nr"), "title": g.Title, "grade": g, "courses": []any{}}, nil
			}},
	)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	a := app.FromEnv()
	a.DownloadDir = t.TempDir()
	s := New(a, r, Config{Token: token, Now: func() time.Time { return now }})
	h := &harness{web: s, srv: httptest.NewServer(s.Handler()), c: &counters{}, app: a, now: &now}
	t.Cleanup(h.srv.Close)

	gradeOf := func(path, body string) map[string]any {
		t.Helper()
		res := decode(t, h.do(t, "POST", path, body, bearer))["result"].(map[string]any)
		if g, ok := res["grade"].(map[string]any); ok {
			return g
		}
		for _, m := range res["overview"].(map[string]any)["modules"].([]any) {
			if m := m.(map[string]any); m["module_nr"] == "I166" {
				m["passed"] = res["stats"].(map[string]any)["passed"]
				return m
			}
		}
		t.Fatal("I166 missing")
		return nil
	}

	if g := gradeOf("/api/tools/cis_grades", `{}`); g["grade"] != "" {
		t.Fatalf("no PDF seen yet: %v", g)
	}
	now = now.Add(time.Minute) // the watcher reads the PDF
	if _, err := s.toolAtMost("cis_transcript_grades", tools.Args{}, 0); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute) // and a fresher page still lags behind
	for _, path := range []string{"/api/tools/cis_grades", "/api/tools/cis_grades?fresh=1"} {
		g := gradeOf(path, `{}`)
		if g["grade"] != "1,7" || g["status"] != grades.StatusPassed || g["source"] != grades.SourceTranscript || g["passed"] != 1.0 {
			t.Errorf("%s: %v", path, g)
		}
	}
	if g := gradeOf("/api/tools/nak_module", `{"module_nr":"I166"}`); g["grade"] != "1,7" || g["source"] != grades.SourceTranscript {
		t.Errorf("module page: %v", g)
	}
	if g := gradeOf("/api/tools/nak_module", `{"module_nr":"I140"}`); g["grade"] != "" {
		t.Errorf("other modules untouched: %v", g)
	}
}
