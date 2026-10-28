package transfer

import (
	"testing"
	"time"
)

func d(s string) time.Time {
	t, _ := time.ParseInLocation("02.01.2006", s, time.UTC)
	return t
}

// quarters as the CIS lists them (lecture quarters)
var quarters = []Quarter{
	{"I/24", d("08.01.2024"), d("15.03.2024")}, {"II/24", d("15.04.2024"), d("21.06.2024")},
	{"III/24", d("29.07.2024"), d("04.10.2024")}, {"IV/24", d("14.10.2024"), d("20.12.2024")},
	{"I/25", d("06.01.2025"), d("14.03.2025")}, {"II/25", d("21.04.2025"), d("27.06.2025")},
	{"III/25", d("04.08.2025"), d("10.10.2025")}, {"IV/25", d("13.10.2025"), d("19.12.2025")},
	{"I/26", d("05.01.2026"), d("13.03.2026")}, {"II/26", d("20.04.2026"), d("26.06.2026")},
	{"III/26", d("27.07.2026"), d("02.10.2026")}, {"IV/26", d("12.10.2026"), d("18.12.2026")},
	{"I/27", d("11.01.2027"), d("19.03.2027")},
}

func TestPlanWirtschaftsinformatik2023(t *testing.T) {
	reports := []Report{
		{No: "1", Abgabedatum: "10.07.2024", Wertung: "bestanden"},
		{No: "2", Abgabedatum: "29.04.2025", Wertung: "bestanden"},
		{No: "3", Abgabedatum: "12.11.2025", Wertung: "bestanden"},
		{No: "4", Abgabedatum: "26.06.2026", Wertung: "bestanden"},
	}
	p := BuildPlan("Wirtschaftsinformatik (B.Sc.)", "I23a", quarters, reports, d("02.10.2026"))
	if !p.Known || p.Cohort != 2023 || len(p.Slots) != 6 {
		t.Fatalf("plan %+v", p)
	}
	// TP 1 belongs to the practical phase after semester 1: II/24
	if s := p.Slots[0]; s.Phase != "II/24" || s.State != "done" {
		t.Errorf("TP1 %+v", s)
	}
	// TP 2 should have been in IV/24, came in April 2025
	if s := p.Slots[1]; s.Phase != "IV/24" || s.State != "late" {
		t.Errorf("TP2 %+v", s)
	}
	// TP 5 was due in II/26 and is missing: behind
	if s := p.Slots[4]; s.State != "behind" {
		t.Errorf("TP5 %+v", s)
	}
	// TP 6 runs now (III/26), latest registration 9 weeks before its end
	if s := p.Slots[5]; s.State != "now" && s.State != "behind" {
		t.Errorf("TP6 %+v", s)
	}
	if p.Behind < 1 {
		t.Errorf("behind = %d", p.Behind)
	}
	// the thesis wants T1–T5: it starts with I/27, so T5 must be under way 9 weeks before
	if p.ThesisFrom != "11.01.2027" || p.ThesisRegisterBy != "09.11.2026" {
		t.Errorf("thesis from %s, register T5 by %s", p.ThesisFrom, p.ThesisRegisterBy)
	}
	if p.Next == nil || p.Next.No != 5 {
		t.Errorf("next %+v", p.Next)
	}
}

func TestPlanOtherProgrammeAndUnknown(t *testing.T) {
	p := BuildPlan("Angewandte Informatik (B.Sc.)", "A24b", quarters, nil, d("01.08.2025"))
	if !p.Known || p.Slots[0].Phase != "III/25" { // AINF: TP 1 after semester 2, July of year 1
		t.Errorf("AINF TP1 phase %q", p.Slots[0].Phase)
	}
	if p := BuildPlan("Master Irgendwas", "M24", quarters, nil, d("01.08.2025")); p.Known {
		t.Error("no plan for programmes without Transferleistungen")
	}
}

// the CIS lists only recent quarters: older phases use the typical starts
func TestPlanWithoutOldQuarters(t *testing.T) {
	recent := quarters[4:] // from I/25
	p := BuildPlan("Wirtschaftsinformatik (B.Sc.)", "I23a", recent, []Report{{No: "1", Abgabedatum: "10.07.2024", Wertung: "bestanden"}}, d("02.10.2026"))
	if s := p.Slots[0]; s.State != "done" || s.PhaseTo != "28.07.2024" {
		t.Errorf("TP1 without CIS dates: %+v", s)
	}
}

// A report whose writing time runs (registered, not submitted) is in progress while its latest
// submission date has not passed — even after its planned practical phase (Tom, 28.10.2026).
func TestRunningReportIsInProgressUntilItsDeadline(t *testing.T) {
	reports := []Report{
		{No: "1", Abgabedatum: "10.07.2024", Wertung: "bestanden"},
		{No: "2", Abgabedatum: "29.04.2025", Wertung: "bestanden"},
		{No: "3", Abgabedatum: "12.11.2025", Wertung: "bestanden"},
		{No: "4", Abgabedatum: "26.06.2026", Wertung: "bestanden"},
		{No: "5", Abgabedatum: "11.11.2026", Status: "Erstellungszeit läuft", Action: "upload"},
	}
	r := reports[4]
	if !r.IsRunning() || reports[0].IsRunning() {
		t.Fatal("running not recognised")
	}
	p := BuildPlan("Wirtschaftsinformatik (B.Sc.)", "I23a", quarters, reports, d("28.10.2026"))
	if s := p.Slots[4]; s.State != "progress" || s.Abgabe != "11.11.2026" || !s.Running {
		t.Fatalf("T5 %+v", s)
	}
	// only T6 (planned for III/26, not started) is behind; T5 runs within its deadline
	if p.Behind != 1 || p.Slots[5].State != "behind" {
		t.Errorf("behind = %d, T6 %+v", p.Behind, p.Slots[5])
	}
	// once the latest submission date has passed without a submission, it is behind
	late := BuildPlan("Wirtschaftsinformatik (B.Sc.)", "I23a", quarters, reports, d("12.11.2026"))
	if s := late.Slots[4]; s.State != "behind" {
		t.Errorf("T5 after its deadline %+v", s)
	}
}
