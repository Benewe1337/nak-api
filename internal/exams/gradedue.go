package exams

import (
	"sort"
	"time"
)

// GradeRule cites where the grading period comes from.
const GradeRule = "PVO § 17 Abs. 3 (Fassung vom 20.08.2026): Bachelor binnen vier Vorlesungswochen (Wochen, in denen irgendein Semester Vorlesungen hat, laut NAK-Vorlesungszeiten), Master binnen sechs Kalenderwochen; eine Soll-Vorschrift"

// GradeDue is the day by which a grade should be published. Bachelor: the
// end (Sunday) of the fourth week with lectures after the exam week — a week
// counts when any semester has lectures (NAK-wide periods, see PeriodDays), not
// only one's own cohort; lectureDays decide;
// ok=false when the plan does not reach four lecture weeks yet. Master: six
// calendar weeks after the exam.
func GradeDue(exam time.Time, lectureDays []time.Time, master bool) (time.Time, bool) {
	if master {
		return exam.AddDate(0, 0, 42), true
	}
	examWeek := weekStart(exam)
	seen := map[time.Time]bool{}
	var weeks []time.Time
	for _, d := range lectureDays {
		w := weekStart(d)
		if !w.After(examWeek) || seen[w] {
			continue
		}
		seen[w] = true
		weeks = append(weeks, w)
	}
	sort.Slice(weeks, func(i, j int) bool { return weeks[i].Before(weeks[j]) })
	if len(weeks) < 4 {
		return time.Time{}, false
	}
	return weeks[3].AddDate(0, 0, 6), true
}

func weekStart(t time.Time) time.Time {
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	off := (int(d.Weekday()) + 6) % 7 // Monday = 0
	return d.AddDate(0, 0, -off)
}

// PeriodDays turns lecture periods (from–to, e.g. the quarters of cis_vorlesungszeiten) into one day
// (the Monday) per week they touch, for GradeDue.
func PeriodDays(periods [][2]time.Time) []time.Time {
	seen := map[time.Time]bool{}
	var out []time.Time
	for _, p := range periods {
		for w := weekStart(p[0]); !w.After(p[1]); w = w.AddDate(0, 0, 7) {
			if !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}
