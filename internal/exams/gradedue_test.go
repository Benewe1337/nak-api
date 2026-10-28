package exams

import (
	"testing"
	"time"
)

func day(s string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02", s, time.UTC)
	return t
}

func TestGradeDueCountsLectureWeeksOnly(t *testing.T) {
	exam := day("2026-10-02") // Friday
	lectures := []time.Time{
		day("2026-10-01"),                    // before the exam: does not count
		day("2026-10-05"), day("2026-10-07"), // week 1
		day("2026-10-12"), // week 2
		// 19.10.–08.11.: Praxisphase, no lectures
		day("2026-11-09"), // week 3
		day("2026-11-17"), // week 4
		day("2026-11-23"), // later
	}
	due, ok := GradeDue(exam, lectures, false)
	if !ok || !due.Equal(day("2026-11-22")) {
		t.Fatalf("due %v ok %v, want end of the 4th lecture week (Sun 22.11.2026)", due, ok)
	}
}

func TestGradeDueUnknownWhenThePlanEndsTooEarly(t *testing.T) {
	_, ok := GradeDue(day("2026-10-02"), []time.Time{day("2026-10-05"), day("2026-10-13")}, false)
	if ok {
		t.Fatal("two known lecture weeks cannot give a deadline of four")
	}
}

func TestGradeDueMasterIsSixCalendarWeeks(t *testing.T) {
	due, ok := GradeDue(day("2026-10-02"), nil, true)
	if !ok || !due.Equal(day("2026-11-13")) {
		t.Fatalf("master due %v", due)
	}
}

// The four lecture weeks run while any semester has lectures (NAK-wide Vorlesungszeiten),
// not only while one's own cohort does (Tom, 28.10.2026).
func TestGradeDueCountsEverySemestersLectureWeeks(t *testing.T) {
	periods := [][2]time.Time{
		{day("2026-07-27"), day("2026-10-02")}, // III/26
		{day("2026-10-12"), day("2026-12-18")}, // IV/26
		{day("2027-01-11"), day("2027-03-19")}, // I/27
	}
	lectures := PeriodDays(periods)

	// Own cohort in its Praxisphase from 05.10.: still IV/26 lectures elsewhere -> 12.10., 19.10., 26.10., 02.11.
	if due, ok := GradeDue(day("2026-10-02"), lectures, false); !ok || !due.Equal(day("2026-11-08")) {
		t.Fatalf("due %v ok %v, want Sun 08.11.2026", due, ok)
	}
	// Over the Christmas break nobody has lectures: 11.01., 18.01., 25.01., 01.02.
	if due, ok := GradeDue(day("2026-12-18"), lectures, false); !ok || !due.Equal(day("2027-02-07")) {
		t.Fatalf("due %v ok %v, want Sun 07.02.2027", due, ok)
	}
}

func TestPeriodDaysGivesEveryWeekOnce(t *testing.T) {
	days := PeriodDays([][2]time.Time{{day("2026-10-14"), day("2026-10-27")}}) // Wed .. Tue of the 3rd week
	if len(days) != 3 || !days[0].Equal(day("2026-10-12")) || !days[2].Equal(day("2026-10-26")) {
		t.Fatalf("days %v", days)
	}
}
