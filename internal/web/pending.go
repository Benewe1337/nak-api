package web

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Raindancer118/nak-api/internal/exams"
	"github.com/Raindancer118/nak-api/internal/tools"
)

// A grade is pending for every exam the owner was registered for, wrote in
// the last 120 days and has not got a result for since. The CIS forgets an
// exam once it is over; the history remembers it.

const pendingWindow = 120 * 24 * time.Hour

var deDate = regexp.MustCompile(`(\d{2}\.\d{2}\.\d{4})(?:\s+(\d{1,2}:\d{2}))?`)

func parseDE(s string, loc *time.Location) (time.Time, bool) {
	m := deDate.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	layout, v := "02.01.2006", m[1]
	if m[2] != "" {
		layout, v = "02.01.2006 15:04", m[1]+" "+m[2]
	}
	t, err := time.ParseInLocation(layout, v, loc)
	return t, err == nil
}

func titleKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "wp ")
	return strings.Join(strings.Fields(s), " ")
}

type pendingExam struct {
	examRecord
	at time.Time
}

func (s *Server) pendingExams(now time.Time) []pendingExam {
	snap := s.history.snapshot()
	var out []pendingExam
	seen := map[string]bool{} // the CIS lists one entry per offered group
	for _, e := range snap.Exams {
		at, ok := parseDE(e.Start, s.app.Zone)
		if !e.Registered || !ok || at.After(now) || now.Sub(at) > pendingWindow {
			continue
		}
		key := titleKey(e.Title) + "|" + e.Start
		if seen[key] || gradedSince(snap.Grades, e, at, s.app.Zone) {
			continue
		}
		seen[key] = true
		out = append(out, pendingExam{e, at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at.Before(out[j].at) })
	return out
}

// gradedSince: a result for this exam's module arrived after the exam. The
// exam's number (A222,I222) is not the module's (I168), so the title links
// them; an elective (WP…) ends up on a "Wahlpflichtmodul n".
func gradedSince(steps map[string][]gradeRecord, e examRecord, at time.Time, loc *time.Location) bool {
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, loc)
	elective := strings.HasPrefix(e.ModuleNr, "WP")
	for nr, list := range steps {
		if len(list) == 0 {
			continue
		}
		title := list[len(list)-1].Title
		same := strings.Contains(","+e.ModuleNr+",", ","+nr+",") || titleKey(title) == titleKey(e.Title) ||
			(elective && strings.HasPrefix(strings.ToLower(title), "wahlpflicht"))
		if !same {
			continue
		}
		for _, g := range list {
			if d, ok := parseDE(g.ExamDate, loc); ok {
				if !d.Before(day) {
					return true
				}
			} else if g.Seen.After(at) {
				return true // from the PDF, which has no exam dates
			}
		}
	}
	return false
}

type pendingGrade struct {
	ExamID   string `json:"exam_id"`
	Manual   bool   `json:"manual,omitempty"`
	ModuleNr string `json:"module_nr"`
	Title    string `json:"title"`
	Written  string `json:"written"`
	Due      string `json:"due,omitempty"`
	DueKnown bool   `json:"due_known"`
	Overdue  bool   `json:"overdue,omitempty"`
	Note     string `json:"note,omitempty"`
}

// pendingAPI adds the deadline: four lecture weeks (Bachelor) — weeks in which any
// semester has lectures (NAK-wide Vorlesungszeiten) plus the own timetable — or six
// calendar weeks (Master).
func (s *Server) pendingAPI(w http.ResponseWriter, r *http.Request) {
	now := s.cfg.Now().In(s.app.Zone)
	list := s.pendingExams(now)
	out := []pendingGrade{}
	if len(list) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"pending": out, "rule": exams.GradeRule})
		return
	}
	master := s.isMaster()
	var lectures []time.Time
	if !master {
		lectures = append(s.lectureDays(list[0].at, list[len(list)-1].at.AddDate(0, 0, 140)), s.periodDays()...)
	}
	for _, p := range list {
		pg := pendingGrade{ExamID: p.ExamID, Manual: p.Manual, ModuleNr: p.ModuleNr, Title: p.Title, Written: p.Start}
		if due, ok := exams.GradeDue(p.at, lectures, master); ok {
			pg.Due, pg.DueKnown = due.Format("02.01.2006"), true
			pg.Overdue = now.After(due.AddDate(0, 0, 1))
		} else {
			pg.Note = "Die bekannten Vorlesungszeiten reichen noch nicht über vier Vorlesungswochen hinaus."
		}
		out = append(out, pg)
	}
	writeJSON(w, http.StatusOK, map[string]any{"pending": out, "rule": exams.GradeRule})
}

func (s *Server) isMaster() bool {
	raw, err := s.cachedTool("cis_status", tools.Args{})
	if err != nil {
		return false
	}
	var st map[string]any
	json.Unmarshal(raw, &st)
	prog := strings.ToLower(str(st, "studiengang"))
	return strings.Contains(prog, "master") || strings.Contains(prog, "m.sc") || strings.Contains(prog, "m.a.") || strings.Contains(prog, "mba")
}

// lectureDays: days with a lecture, exercise, elective or tutorial in the
// timetable; exams (K, P) do not make a lecture week.
// periodDays: one day per week of the NAK-wide lecture periods (cis_vorlesungszeiten), nil if unknown.
func (s *Server) periodDays() []time.Time {
	raw, err := s.cachedTool("cis_vorlesungszeiten", tools.Args{})
	if err != nil {
		return nil
	}
	var vz struct {
		Quarters []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"quarters"`
	}
	json.Unmarshal(raw, &vz)
	var periods [][2]time.Time
	for _, q := range vz.Quarters {
		from, ok1 := parseDE(q.From, s.app.Zone)
		to, ok2 := parseDE(q.To, s.app.Zone)
		if ok1 && ok2 {
			periods = append(periods, [2]time.Time{from, to})
		}
	}
	return exams.PeriodDays(periods)
}

func (s *Server) lectureDays(from, to time.Time) []time.Time {
	raw, err := s.cachedTool("cis_timetable", tools.Args{"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02")})
	if err != nil {
		return nil
	}
	var tt struct {
		Events []struct {
			Start time.Time `json:"start"`
			Kind  string    `json:"kind"`
		} `json:"events"`
	}
	json.Unmarshal(raw, &tt)
	var days []time.Time
	for _, e := range tt.Events {
		if e.Kind == "K" || e.Kind == "P" || e.Start.IsZero() {
			continue
		}
		days = append(days, e.Start.In(s.app.Zone))
	}
	return days
}
