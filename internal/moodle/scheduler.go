package moodle

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Raindancer118/nak-api/internal/htmlx"
	"golang.org/x/net/html"
)

// ErrSchedulerLayout means the page is not a mod/scheduler student view any
// more (or not logged in) — Moodle changed or the session is wrong.
var ErrSchedulerLayout = errors.New("the Moodle scheduler page no longer has the expected structure")

// Slot is one row of the scheduler's student view.
type Slot struct {
	Status            string // booked | past | available
	Start, End        string
	Weekday           string
	Minutes           int
	Teacher           string
	Location, Notes   string
	SlotID            int64
	OtherParticipants int
	Cancellable       bool
}

type SchedulerPage struct {
	Name    string
	Message string
	Slots   []Slot
}

var months = map[string]time.Month{
	"januar": 1, "februar": 2, "märz": 3, "april": 4, "mai": 5, "juni": 6, "juli": 7, "august": 8, "september": 9,
	"oktober": 10, "november": 11, "dezember": 12,
	"january": 1, "february": 2, "march": 3, "may": 5, "june": 6, "july": 7, "october": 10, "december": 12,
}

var (
	dateRe  = regexp.MustCompile(`(\d{1,2})\.?\s+([\p{L}]+)\.?\s+(\d{4})`)
	timeRe  = regexp.MustCompile(`(\d{1,2}):(\d{2})\s*([AaPp][Mm])?`)
	pastRe  = regexp.MustCompile(`(?i)früher|vergangen|past|previous|attended`)
	availRe = regexp.MustCompile(`(?i)verfügbar|available|buchbar|bookable`)
)

// ParseScheduler reads the logged-in view.php of a scheduler activity. Table
// columns are recognised by header words and cell content, never by position
// alone: the teacher column carries a per-site role label.
func ParseScheduler(body string, zone *time.Location) (*SchedulerPage, error) {
	doc := htmlx.MustParse(body)
	attr := func(k, v string) htmlx.Match { return func(n *html.Node) bool { return htmlx.Attr(n, k) == v } }
	if htmlx.First(doc, htmlx.And(htmlx.Tag("div"), attr("role", "main"))) == nil ||
		htmlx.First(doc, htmlx.And(htmlx.Tag("input"), attr("name", "logintoken"))) != nil {
		return nil, ErrSchedulerLayout
	}
	p := &SchedulerPage{}
	if h := htmlx.First(doc, htmlx.Tag("h1")); h != nil {
		p.Name = htmlx.Text(h)
	}
	if p.Name == "" {
		if t := htmlx.First(doc, htmlx.Tag("title")); t != nil {
			p.Name = strings.TrimSpace(strings.SplitN(htmlx.Text(t), "|", 2)[0])
		}
	}
	if d := htmlx.First(doc, htmlx.Class("studentbookingmessage")); d != nil {
		p.Message = htmlx.Text(d)
	}
	heading := ""
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "h2", "h3", "h4":
				heading = htmlx.Text(n)
			case "table":
				p.Slots = append(p.Slots, parseSlotTable(n, heading, zone)...)
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return p, nil
}

func parseSlotTable(table *html.Node, heading string, zone *time.Location) []Slot {
	const (
		colOther = iota + 1
		colLocation
		colNotes
	)
	cols := map[int]int{}
	if hr := htmlx.First(table, htmlx.Tag("tr")); hr != nil {
		for i, c := range htmlx.Cells(hr) {
			t := strings.ToLower(htmlx.Text(c))
			switch {
			case containsAny(t, "andere", "other", "teilnehm", "participant", "student"):
				cols[i] = colOther
			case containsAny(t, "ort", "location", "room", "raum", "place"):
				cols[i] = colLocation
			case containsAny(t, "kommentar", "comment", "note", "notiz", "bemerk"):
				cols[i] = colNotes
			}
		}
	}
	var out []Slot
	for _, tr := range htmlx.Rows(table) {
		if htmlx.First(tr, htmlx.Class("datelabel")) == nil {
			continue
		}
		s := Slot{}
		for i, c := range htmlx.Cells(tr) {
			switch cols[i] {
			case colOther:
				s.OtherParticipants = len(htmlx.All(c, htmlx.Class("otherstudent"))) - len(htmlx.All(c, htmlx.And(htmlx.Class("otherstudent"), htmlx.Class("highlight"))))
			case colLocation:
				s.Location = htmlx.Text(c)
			case colNotes:
				s.Notes = htmlx.Text(c)
			default:
				if s.Teacher == "" && htmlx.First(c, htmlx.HrefContains("/user/view.php")) != nil {
					s.Teacher = htmlx.Text(c)
				}
			}
		}
		fillTimes(&s, htmlx.Text(htmlx.First(tr, htmlx.Class("datelabel"))), htmlx.Text(htmlx.First(tr, htmlx.Class("timelabel"))), zone)
		s.Status, s.Cancellable, s.SlotID = rowStatus(tr, heading)
		out = append(out, s)
	}
	return out
}

// rowStatus: the cancel form marks an own booking; otherwise the section
// heading decides, and rows with a book button count as available.
func rowStatus(tr *html.Node, heading string) (status string, cancellable bool, slotID int64) {
	hasBook := false
	for _, f := range htmlx.All(tr, htmlx.Tag("form")) {
		what, id := "", int64(0)
		for _, in := range htmlx.All(f, htmlx.Tag("input")) {
			switch htmlx.Attr(in, "name") {
			case "what":
				what = htmlx.Attr(in, "value")
			case "slotid":
				id, _ = strconv.ParseInt(htmlx.Attr(in, "value"), 10, 64)
			}
		}
		if id != 0 {
			slotID = id
		}
		if what == "cancelbooking" {
			cancellable = true
		} else if what != "" {
			hasBook = true
		}
	}
	switch {
	case cancellable:
		return "booked", true, slotID
	case pastRe.MatchString(heading):
		return "past", false, slotID
	case hasBook || availRe.MatchString(heading):
		return "available", false, slotID
	default:
		return "booked", false, slotID
	}
}

func fillTimes(s *Slot, dateText, timeText string, zone *time.Location) {
	dm := dateRe.FindStringSubmatch(dateText)
	if dm == nil {
		return
	}
	mon, ok := months[strings.ToLower(dm[2])]
	if !ok {
		return
	}
	day, _ := strconv.Atoi(dm[1])
	year, _ := strconv.Atoi(dm[3])
	if i := strings.Index(dateText, ","); i > 0 {
		s.Weekday = strings.TrimSpace(dateText[:i])
	}
	var stamps []time.Time
	for _, tm := range timeRe.FindAllStringSubmatch(timeText, 2) {
		h, _ := strconv.Atoi(tm[1])
		mi, _ := strconv.Atoi(tm[2])
		switch strings.ToLower(tm[3]) {
		case "pm":
			if h < 12 {
				h += 12
			}
		case "am":
			if h == 12 {
				h = 0
			}
		}
		stamps = append(stamps, time.Date(year, mon, day, h, mi, 0, 0, zone))
	}
	if len(stamps) == 0 {
		s.Start = fmt.Sprintf("%04d-%02d-%02d", year, mon, day)
		return
	}
	const layout = "2006-01-02 15:04"
	s.Start = stamps[0].Format(layout)
	if len(stamps) > 1 {
		s.End = stamps[1].Format(layout)
		s.Minutes = int(stamps[1].Sub(stamps[0]).Minutes())
	}
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// Scheduler returns the own bookings (and, as far as Moodle lists them, past
// and still bookable slots) of a scheduler activity. Read-only.
func (s *Service) Scheduler(cmid int64) (*M, error) {
	if cmid <= 0 {
		return nil, argErr("cmid is required (module id from moodle_course_contents, the id in …/mod/scheduler/view.php?id=)")
	}
	path := fmt.Sprintf("/mod/scheduler/view.php?id=%d", cmid)
	body, err := s.C.WebGet(path)
	if err != nil {
		return nil, err
	}
	p, err := ParseScheduler(body, s.Zone)
	if err != nil {
		return nil, err
	}
	groups := map[string][]*M{}
	for _, sl := range p.Slots {
		x := m("start", sl.Start, "end", sl.End, "weekday", sl.Weekday, "duration_minutes", sl.Minutes, "teacher", sl.Teacher,
			"location", sl.Location, "notes", sl.Notes, "slotid", sl.SlotID)
		if sl.Status == "booked" {
			x.Put("other_participants", sl.OtherParticipants).Put("cancellable", sl.Cancellable)
		}
		groups[sl.Status] = append(groups[sl.Status], x)
	}
	return m("cmid", cmid, "name", p.Name, "url", s.C.Base+path, "booked", groups["booked"], "past", groups["past"],
		"available", groups["available"], "message", p.Message), nil
}
