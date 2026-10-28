package transfer

import (
	"math"
	"os"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		// Fixtures hold personal CIS data and are git-ignored; skip when absent.
		t.Skipf("fixture %s not present (kept local for privacy): %v", name, err)
	}
	return string(b)
}

func TestParseList(t *testing.T) {
	reports := parseList(fixture(t, "liste.html"), "https://cis.nordakademie.de")
	if len(reports) == 0 {
		t.Fatal("expected reports, got none")
	}
	first := reports[0]
	if first.No != "1" {
		t.Errorf("no = %q, want 1", first.No)
	}
	if first.Abgabedatum != "10.07.2024" {
		t.Errorf("abgabedatum = %q", first.Abgabedatum)
	}
	if first.Wertung != "bestanden" {
		t.Errorf("wertung = %q, want bestanden", first.Wertung)
	}
	if first.Status != "bewertet" {
		t.Errorf("status = %q, want bewertet", first.Status)
	}
	if first.Action != "performance" {
		t.Errorf("action = %q, want performance", first.Action)
	}
	if first.ID != "14534" {
		t.Errorf("id = %q, want 14534", first.ID)
	}
	if first.Topic == "" || first.Module == "" {
		t.Errorf("topic/module empty: %+v", first)
	}
	if first.Running {
		t.Error("a graded report counts as running")
	}
	running := 0
	for _, r := range reports {
		if r.Running {
			running++
			if r.Abgabedatum == "" || r.Wertung != "" {
				t.Errorf("running report without deadline or with a result: %+v", r)
			}
		}
	}
	if running != 1 {
		t.Errorf("running = %d, want the one whose Erstellungszeit läuft", running)
	}
}

func TestParseBewertung(t *testing.T) {
	b := parseBewertung(fixture(t, "bewertung.html"), "https://cis.nordakademie.de")

	if b.Fields["Matrikel-Nr."] != "99999" {
		t.Errorf("Matrikel-Nr. = %q", b.Fields["Matrikel-Nr."])
	}
	if b.Fields["Thema"] == "" {
		t.Error("Thema not parsed")
	}
	if want := "Beispielmodul C (M300)"; b.Fields["Modul"] != want {
		t.Errorf("Modul = %q, want %q", b.Fields["Modul"], want)
	}
	if len(b.Kriterien) < 4 {
		t.Fatalf("expected several criteria, got %d", len(b.Kriterien))
	}
	for _, k := range b.Kriterien {
		if k.Kriterium == "" || k.Note == "" || k.Gewichtung == "" {
			t.Errorf("incomplete criterion: %+v", k)
		}
	}
}

func TestWeightedAverage(t *testing.T) {
	// All notes 2.0 with any weights must average to exactly 2.0.
	ks := []Kriterium{
		{Note: "2.0", Gewichtung: "10 %"},
		{Note: "2.0", Gewichtung: "5 %"},
		{Note: "2.0", Gewichtung: "85 %"},
	}
	avg, ok := WeightedAverage(ks)
	if !ok {
		t.Fatal("expected ok")
	}
	if math.Abs(avg-2.0) > 1e-9 {
		t.Errorf("avg = %v, want 2.0", avg)
	}

	// Mixed: (1.0*50 + 3.0*50)/100 = 2.0
	ks2 := []Kriterium{
		{Note: "1,0", Gewichtung: "50%"},
		{Note: "3.0", Gewichtung: "50 %"},
	}
	avg2, _ := WeightedAverage(ks2)
	if math.Abs(avg2-2.0) > 1e-9 {
		t.Errorf("avg2 = %v, want 2.0", avg2)
	}

	// Nothing parseable -> ok=false
	if _, ok := WeightedAverage([]Kriterium{{Note: "n/a", Gewichtung: "x"}}); ok {
		t.Error("expected ok=false for unparseable input")
	}
}

func TestBewertungGesamtnoteComputed(t *testing.T) {
	b := parseBewertung(fixture(t, "bewertung.html"), "https://cis.nordakademie.de")
	if !b.HasGesamtnote {
		t.Fatal("expected a computed Gesamtnote from the fixture criteria")
	}
	// All criteria in the (anonymised) fixture carry note 2.0, so the weighted
	// average is exactly 2.0 regardless of the individual weightings.
	if math.Abs(b.Gesamtnote-2.0) > 1e-9 {
		t.Errorf("Gesamtnote = %v, want 2.0", b.Gesamtnote)
	}
	if len(b.Kriterien) != 12 {
		t.Errorf("expected 12 criteria, got %d", len(b.Kriterien))
	}
}
