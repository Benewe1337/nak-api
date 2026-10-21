package grades

import (
	"os"
	"strings"
	"testing"
)

func TestParseTranscriptText(t *testing.T) {
	b, err := os.ReadFile("testdata/notenuebersicht.txt")
	if err != nil {
		t.Fatal(err)
	}
	tg, err := ParseTranscriptText(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if tg.Average != "2,4" || tg.Date != "02.10.2026" {
		t.Errorf("average %q date %q", tg.Average, tg.Date)
	}
	want := map[string][2]string{ // nr → grade, credits
		"I140": {"3,7", "5"},
		"I166": {"1,7", "6"},
		"I168": {"4,0 (2. Versuch)", ""},
		"I179": {"bestanden", "6"},
		"I201": {"2,0 *", "5"},
	}
	got := map[string]TranscriptGrade{}
	for _, g := range tg.Modules {
		got[g.ModuleNr] = g
	}
	for nr, w := range want {
		g, ok := got[nr]
		if !ok || g.Grade != w[0] || g.Credits != w[1] {
			t.Errorf("%s = %+v, want grade %q credits %q", nr, g, w[0], w[1])
		}
	}
	if _, ok := got["I151"]; ok {
		t.Error("modules without an exam (#) are not grades")
	}
	if g := got["I166"]; g.Title != "Einführung in die objektorientierte Programmierung" {
		t.Errorf("title %q", g.Title)
	}
	if _, err := ParseTranscriptText("irgendein anderes PDF"); err == nil {
		t.Error("a PDF without the grade table must be reported as drift")
	}
}

func TestSameGrade(t *testing.T) {
	for _, c := range [][2]string{{"4,0 (2. Versuch)", "4,0 (2.Versuch)"}, {"1,7", " 1,7 "}, {"2,0 *", "2,0"}, {"bestanden", "Bestanden"}} {
		if NormalizeGrade(c[0]) != NormalizeGrade(c[1]) {
			t.Errorf("%q and %q should be the same grade", c[0], c[1])
		}
	}
	if NormalizeGrade("4,0 (2. Versuch)") == NormalizeGrade("4,0") {
		t.Error("a retake is a different result than the first attempt")
	}
}

func TestApplyTranscript(t *testing.T) {
	o := &Overview{Modules: []ModuleGrade{
		{ModuleNr: "I140", Title: "Automatentheorie", Grade: "3,7", Status: StatusPassed, Credits: "5", EntryDate: "01.09.2026"},
		{ModuleNr: "I166", Title: "OOP", Status: StatusOpen, Credits: "6", ExamDate: "25.09.2026"},
		{ModuleNr: "I168", Title: "Diskrete Mathematik 2", Grade: "5,0", Status: StatusFailed, ExamDate: "01.03.2026", EntryDate: "10.03.2026"},
		{ModuleNr: "A222,I222", Title: "Diskrete Mathematik 3", Status: StatusOpen},
	}}
	tg := &TranscriptGrades{Date: "02.10.2026", Modules: []TranscriptGrade{
		{ModuleNr: "I140", Title: "Automatentheorie", Grade: "3,7", Credits: "5"},
		{ModuleNr: "I166", Title: "OOP", Grade: "1,7", Credits: "6"},
		{ModuleNr: "I168", Title: "Diskrete Mathematik 2", Grade: "4,0 (2. Versuch)"},
		{ModuleNr: "I179", Title: "Wissenschaftliches Arbeiten 1", Grade: "bestanden", Credits: "6"},
		{ModuleNr: "I222", Title: "Diskrete Mathematik 3", Grade: "2,3"},
	}}
	older := &Overview{Modules: append([]ModuleGrade(nil), o.Modules...)}
	if c := older.ApplyTranscript(tg, false); strings.Join(c, ",") != "I166,I179,I222" || older.Find("I168").Grade != "5,0" {
		t.Errorf("an older PDF only fills gaps: %v %+v", c, older.Find("I168"))
	}
	changed := o.ApplyTranscript(tg, true)
	if strings.Join(changed, ",") != "I166,I168,I179,I222" {
		t.Fatalf("changed %v", changed)
	}
	if m := o.Find("I140"); m.Source != "" || m.EntryDate != "01.09.2026" {
		t.Errorf("same grade must stay untouched: %+v", m)
	}
	m := o.Find("I166")
	if m.Grade != "1,7" || m.GradeValue == nil || *m.GradeValue != 1.7 || m.Status != StatusPassed || m.Source != SourceTranscript {
		t.Errorf("new grade: %+v", m)
	}
	if m.ExamDate != "25.09.2026" || m.EntryDate != "02.10.2026" {
		t.Errorf("exam date kept, entry date from the PDF: %+v", m)
	}
	m = o.Find("I168")
	if m.Attempt != 2 || m.Status != StatusPassed || m.ExamDate != "" || m.EntryDate != "02.10.2026" {
		t.Errorf("changed grade drops the old attempt's dates: %+v", m)
	}
	if m := o.Find("I179"); m == nil || m.Status != StatusPassed || m.Credits != "6" || m.Source != SourceTranscript {
		t.Errorf("module missing from the page is added: %+v", m)
	}
	if len(o.Modules) != 5 || o.Modules[3].Grade != "2,3" {
		t.Errorf("one of several numbers matches the row: %+v", o.Modules)
	}
	if st := o.Stats(); st.Passed != 5 || st.Open != 0 {
		t.Errorf("stats see the PDF grades: %+v", st)
	}
}
