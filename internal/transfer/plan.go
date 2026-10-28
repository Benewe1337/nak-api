package transfer

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Where each Transferleistung belongs in the study plan, from the NORDAKADEMIE
// "Idealtypische Verteilung der Transferleistungen" (Moodle course
// Transferleistungen, Studienverlaufspläne): each is written in a practical
// phase, taking about nine weeks from the Auftragsklärung to the assessment.
// Studies start in October; "year 1" is the calendar year after that.

type Quarter struct {
	Name     string // "II/26"
	From, To time.Time
}

type slotAt struct{ year, quarter int }

// TP 1–6 per programme family; TP 6 sits at the end of semester 6.
var plans = map[string][6]slotAt{
	"winf": {{1, 2}, {1, 4}, {2, 3}, {3, 1}, {3, 2}, {3, 3}}, // BWL, Wirtschaftsinformatik
	"ainf": {{1, 3}, {2, 1}, {2, 3}, {3, 1}, {3, 2}, {3, 3}}, // Angewandte/Technische Informatik, Wirtschaftsingenieurwesen
}

var thesisAt = slotAt{4, 1}

const tlWeeks = 9

func planFor(programme string) string {
	p := strings.ToLower(programme)
	switch {
	case strings.Contains(p, "master") || strings.Contains(p, "m.sc") || strings.Contains(p, "mba"):
		return ""
	case strings.Contains(p, "wirtschaftsinformatik") || strings.Contains(p, "betriebswirtschaft") || strings.Contains(p, "bwl"):
		return "winf"
	case strings.Contains(p, "informatik") || strings.Contains(p, "software") || strings.Contains(p, "it-engineering") || strings.Contains(p, "wirtschaftsingenieur"):
		return "ainf"
	}
	return ""
}

type PlanSlot struct {
	No         int    `json:"no"`
	Phase      string `json:"phase"`       // "II/26"
	PhaseFrom  string `json:"phase_from"`  // dd.mm.yyyy
	PhaseTo    string `json:"phase_to"`    // end of the practical phase: the ideal finish
	RegisterBy string `json:"register_by"` // latest start (Auftragsklärung) to finish in time
	// done (passed in its phase), late (passed after it), progress (in the
	// CIS, not passed yet), behind (its phase is over, not passed), now (its
	// phase runs), upcoming
	State    string `json:"state"`
	Tight    bool   `json:"tight,omitempty"` // phase runs but the nine weeks no longer fit
	Abgabe   string `json:"abgabe,omitempty"`
	Wertung  string `json:"wertung,omitempty"`
	WeeksOff int    `json:"weeks_off,omitempty"` // late: weeks after the phase end
	Running  bool   `json:"running,omitempty"`   // writing time runs, not submitted yet (see Report.IsRunning)
}

type Plan struct {
	Known            bool       `json:"known"`
	Programme        string     `json:"programme"`
	Cohort           int        `json:"cohort"`
	Slots            []PlanSlot `json:"slots"`
	Behind           int        `json:"behind"` // phases over without a passed Transferleistung
	Next             *PlanSlot  `json:"next,omitempty"`
	ThesisFrom       string     `json:"thesis_from,omitempty"`        // ideal start of the bachelor thesis
	ThesisRegisterBy string     `json:"thesis_register_by,omitempty"` // latest start of the last of T1–T5 for it
	// latest registration of the thesis it is measured against (planning aid
	// of the cohort or computed for the next bachelor graduation)
	ThesisRegisterUntil string `json:"thesis_register_until,omitempty"`
	Source              string `json:"source"`
}

var cohortRe = regexp.MustCompile(`(\d{2})`)
var quarterRe = regexp.MustCompile(`^(I{1,3}|IV)/(\d{2})$`)

// BuildPlan places the reports on the ideal plan of the programme. zenturie
// ("I23a") gives the cohort; quarters (cis_vorlesungszeiten) the dates.
func BuildPlan(programme, zenturie string, quarters []Quarter, reports []Report, now time.Time) Plan {
	out := Plan{Programme: programme, Source: "NORDAKADEMIE, Idealtypische Verteilung der Transferleistungen (Moodle-Kurs Transferleistungen); je TL etwa 9 Wochen von der Auftragsklärung bis zur Bewertung"}
	key := planFor(programme)
	m := cohortRe.FindStringSubmatch(zenturie)
	if key == "" || m == nil {
		return out
	}
	cohort, _ := strconv.Atoi(m[1])
	out.Cohort, out.Known = 2000+cohort, true
	start := func(s slotAt) (string, time.Time, time.Time) {
		return quarterSpan(out.Cohort+s.year, s.quarter, quarters, now.Location())
	}
	byNo := map[int]Report{}
	for _, r := range reports {
		if n, err := strconv.Atoi(strings.TrimSpace(r.No)); err == nil {
			byNo[n] = r
		}
	}
	for i, at := range plans[key] {
		name, from, to := start(at)
		s := PlanSlot{No: i + 1, Phase: name, PhaseFrom: de(from), PhaseTo: de(to), RegisterBy: de(to.AddDate(0, 0, -7*tlWeeks))}
		r, have := byNo[i+1]
		passed := have && strings.Contains(strings.ToLower(r.Wertung), "bestanden") && !strings.Contains(strings.ToLower(r.Wertung), "nicht")
		if have {
			s.Abgabe, s.Wertung = r.Abgabedatum, r.Wertung
		}
		running := have && !passed && r.IsRunning()
		s.Running = running
		deadline, hasDeadline := time.Time{}, false
		if running {
			if a, err := time.ParseInLocation("02.01.2006", strings.TrimSpace(r.Abgabedatum), now.Location()); err == nil {
				deadline, hasDeadline = a.AddDate(0, 0, 1), true // the whole submission day counts
			}
		}
		switch {
		case passed:
			s.State = "done"
			if a, err := time.ParseInLocation("02.01.2006", strings.TrimSpace(r.Abgabedatum), now.Location()); err == nil && a.After(to) {
				s.State, s.WeeksOff = "late", int(a.Sub(to).Hours()/24/7+0.5)
			}
		case running && hasDeadline && now.Before(deadline):
			s.State = "progress"
		case now.After(to):
			s.State = "behind"
		case have:
			s.State = "progress"
		case !now.Before(from):
			s.State, s.Tight = "now", now.After(to.AddDate(0, 0, -7*tlWeeks))
		default:
			s.State = "upcoming"
		}
		if s.State == "behind" {
			out.Behind++
		}
		out.Slots = append(out.Slots, s)
	}
	for i := range out.Slots {
		if st := out.Slots[i].State; st != "done" && st != "late" {
			out.Next = &out.Slots[i]
			break
		}
	}
	_, thesis, _ := start(thesisAt)
	out.ThesisFrom, out.ThesisRegisterBy = de(thesis), de(thesis.AddDate(0, 0, -7*tlWeeks))
	return out
}

// quarterSpan: name, start and end of a practical phase. A phase fills its
// quarter up to the start of the next one; quarters the CIS no longer lists
// fall back to calendar quarters.
func quarterSpan(year, q int, quarters []Quarter, loc *time.Location) (string, time.Time, time.Time) {
	roman := []string{"I", "II", "III", "IV"}
	name := fmt.Sprintf("%s/%02d", roman[q-1], year%100)
	find := func(n string) (Quarter, bool) {
		for _, x := range quarters {
			if x.Name == n {
				return x, true
			}
		}
		return Quarter{}, false
	}
	ny, nq := year, q+1
	if nq == 5 {
		ny, nq = year+1, 1
	}
	next := fmt.Sprintf("%s/%02d", roman[nq-1], ny%100)
	// typical quarter starts at the NORDAKADEMIE (the CIS lists only recent years)
	typical := func(y, q int) time.Time {
		md := [4][2]int{{1, 8}, {4, 18}, {7, 29}, {10, 13}}[q-1]
		return time.Date(y, time.Month(md[0]), md[1], 0, 0, 0, 0, loc)
	}
	from := typical(year, q)
	to := typical(ny, nq).AddDate(0, 0, -1)
	if x, ok := find(name); ok {
		from = x.From
	}
	if x, ok := find(next); ok {
		to = x.From.AddDate(0, 0, -1)
	} else if x, ok := find(name); ok && !x.To.IsZero() {
		to = x.To.AddDate(0, 0, 14)
	}
	return name, from, to
}

// ParseQuarters reads cis_vorlesungszeiten ("II/26", "20.04.2026", "26.06.2026").
func ParseQuarter(name, from, to string, loc *time.Location) (Quarter, bool) {
	if !quarterRe.MatchString(strings.TrimSpace(name)) {
		return Quarter{}, false
	}
	f, err1 := time.ParseInLocation("02.01.2006", from, loc)
	t, err2 := time.ParseInLocation("02.01.2006", to, loc)
	if err1 != nil || err2 != nil {
		return Quarter{}, false
	}
	return Quarter{Name: strings.TrimSpace(name), From: f, To: t}, true
}

func de(t time.Time) string { return t.Format("02.01.2006") }
