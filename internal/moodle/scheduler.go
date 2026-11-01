package moodle

import (
	"errors"
	"fmt"
	"net/url"
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
	Form              *SlotForm // the page's own book/cancel button, if any
}

// SlotForm is the single_button form Moodle renders next to a slot: POST to
// Action with hidden Fields (id, sesskey, what, slotid [, appointgroup]).
type SlotForm struct {
	Action string
	What   string
	Fields []FormField
}

type FormField struct{ Name, Value string }

func (f *SlotForm) get(name string) string {
	for _, x := range f.Fields {
		if x.Name == name {
			return x.Value
		}
	}
	return ""
}

type SchedulerPage struct {
	Name    string
	Message string
	Alerts  []string // error notifications Moodle shows after a rejected action
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
	pastRe  = regexp.MustCompile(`(?i)früher|vergangen|past|previous|attended|besucht|wahrgenommen`)
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
	for _, a := range htmlx.All(doc, htmlx.Class("alert-danger")) {
		if t := htmlx.Text(a); t != "" {
			p.Alerts = append(p.Alerts, t)
		}
	}
	heading := ""
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "h2", "h3", "h4":
				heading = htmlx.Text(n)
			case "table":
				if htmlx.ID("slotbookertable")(n) {
					p.Slots = append(p.Slots, parseBookerTable(n, heading, zone)...)
				} else {
					p.Slots = append(p.Slots, parseSlotTable(n, heading, zone)...)
				}
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
		applyRowState(&s, tr, heading)
		out = append(out, s)
	}
	return out
}

// rowForm returns the row's book/cancel form.
func rowForm(tr *html.Node) *SlotForm {
	for _, f := range htmlx.All(tr, htmlx.Tag("form")) {
		sf := &SlotForm{Action: htmlx.Attr(f, "action")}
		for _, in := range htmlx.All(f, htmlx.Tag("input")) {
			if htmlx.Attr(in, "type") == "hidden" {
				sf.Fields = append(sf.Fields, FormField{htmlx.Attr(in, "name"), htmlx.Attr(in, "value")})
			}
		}
		sf.What = sf.get("what")
		if sf.What != "" {
			return sf
		}
	}
	return nil
}

// applyRowState sets status, slot id and form: the cancel form marks an own
// booking; otherwise the section heading decides, and rows with a book button
// count as available.
func applyRowState(s *Slot, tr *html.Node, heading string) {
	s.Form = rowForm(tr)
	hasBook := false
	if s.Form != nil {
		s.SlotID, _ = strconv.ParseInt(s.Form.get("slotid"), 10, 64)
		s.Cancellable = s.Form.What == "cancelbooking"
		hasBook = !s.Cancellable
	}
	switch {
	case s.Cancellable:
		s.Status = "booked"
	case pastRe.MatchString(heading):
		s.Status = "past"
	case hasBook || availRe.MatchString(heading):
		s.Status = "available"
	default:
		s.Status = "booked"
	}
}

// parseBookerTable reads #slotbookertable (render_scheduler_slot_booker):
// Date | Start | End | Location | Comments | Teacher | Group session | button.
// The date cell is blank when it repeats the previous row.
func parseBookerTable(table *html.Node, heading string, zone *time.Location) []Slot {
	var out []Slot
	date := ""
	for _, tr := range htmlx.Rows(table) {
		c := htmlx.Cells(tr)
		if len(c) < 6 {
			continue
		}
		if d := htmlx.Text(c[0]); d != "" {
			date = d
		}
		s := Slot{Location: htmlx.Text(c[3]), Notes: htmlx.Text(c[4]), Teacher: htmlx.Text(c[5])}
		fillTimes(&s, date, htmlx.Text(c[1])+" – "+htmlx.Text(c[2]), zone)
		applyRowState(&s, tr, heading)
		if s.Status == "booked" { // this table only ever lists bookable slots
			s.Status = "available"
		}
		out = append(out, s)
	}
	return out
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
		switch sl.Status {
		case "booked":
			x.Put("other_participants", sl.OtherParticipants).Put("cancellable", sl.Cancellable)
		case "available":
			x.Put("bookable", sl.Form != nil && sl.Form.What == "bookslot")
		}
		groups[sl.Status] = append(groups[sl.Status], x)
	}
	return m("cmid", cmid, "name", p.Name, "url", s.C.Base+path, "booked", groups["booked"], "past", groups["past"],
		"available", groups["available"], "message", p.Message), nil
}

// ── booking and cancelling (write) ──────────────────────────────────────────
//
// Request shape. Cancel: verified against the live page (single_button form,
// POST view.php with id, sesskey, what=cancelbooking, slotid). Book: derived
// from upstream mod_scheduler (renderer.php render_scheduler_slot_booker,
// studentview.controller.php): the same form with what=bookslot, or
// what=bookingform for schedulers that ask for extra data (refused here).
// Success of both is a 303 back to view.php; failures show an alert there.
// nak never builds the request itself: it replays the form the page offers, so
// hidden fields such as appointgroup travel along.

const schedulerPath = "/mod/scheduler/view.php"

var invalidSesskey = regexp.MustCompile(`(?i)invalidsesskey|sitzungsschlüssel|session ?key`)

func (s *Service) SchedulerBook(cmid, slotID int64, confirm bool) (*M, error) {
	return s.schedulerAct(cmid, slotID, confirm, true)
}

func (s *Service) SchedulerCancel(cmid, slotID int64, confirm bool) (*M, error) {
	return s.schedulerAct(cmid, slotID, confirm, false)
}

func (s *Service) schedulerAct(cmid, slotID int64, confirm, book bool) (*M, error) {
	if cmid <= 0 || slotID <= 0 {
		return nil, argErr("cmid and slotid are required (see moodle_scheduler)")
	}
	for attempt := 0; ; attempt++ {
		p, err := s.schedulerPage(cmid)
		if err != nil {
			return nil, err
		}
		slot, err := s.pickSlot(p, slotID, book)
		if err != nil {
			return nil, err
		}
		if err := s.checkForm(slot.Form, cmid, slotID); err != nil {
			return nil, err
		}
		action := "Terminplaner-Buchung stornieren"
		if book {
			action = "Terminplaner-Zeitfenster buchen"
		}
		details := m("start", slot.Start, "end", slot.End, "teacher", slot.Teacher, "location", slot.Location, "notes", slot.Notes, "slotid", slotID)
		if !confirm {
			return preview(action, "method", "POST", "url", slot.Form.Action, "fields", visibleFields(slot.Form), "slot", details,
				"note", "sesskey wird beim Senden aus der Seite übernommen"), nil
		}
		fields := slot.Form.Fields
		body, err := s.C.WebPost(slot.Form.Action, fields)
		if errors.Is(err, ErrSessionExpired) && attempt == 0 {
			continue // relogin happens on the next page read, which also yields a fresh sesskey
		}
		if err != nil {
			return nil, err
		}
		return s.verifyAct(body, slotID, book, action, details)
	}
}

func (s *Service) schedulerPage(cmid int64) (*SchedulerPage, error) {
	body, err := s.C.WebGet(fmt.Sprintf("%s?id=%d", schedulerPath, cmid))
	if err != nil {
		return nil, err
	}
	return ParseScheduler(body, s.Zone)
}

func (s *Service) pickSlot(p *SchedulerPage, slotID int64, book bool) (*Slot, error) {
	var found *Slot
	var ids []string
	unidentified := 0 // own bookings shown without a form, hence without slot id
	for i := range p.Slots {
		sl := &p.Slots[i]
		if sl.Status == "booked" && sl.SlotID == 0 {
			unidentified++
		}
		if sl.SlotID == slotID {
			found = sl
		}
		if sl.SlotID != 0 && (sl.Status == "available" && book || sl.Status == "booked" && !book) {
			ids = append(ids, strconv.FormatInt(sl.SlotID, 10))
		}
	}
	if book {
		switch {
		case found != nil && found.Status == "booked":
			return nil, argErr("slot %d is already booked by you", slotID)
		case found == nil || found.Status == "past":
			msg := p.Message
			if len(ids) == 0 && msg != "" {
				return nil, argErr("slot %d is not bookable: Moodle lists no free slot (%s)", slotID, msg)
			}
			return nil, argErr("slot %d is not among the bookable slots (bookable: %s)", slotID, strings.Join(ids, ", "))
		case found.Form.What == "bookingform":
			return nil, argErr("this scheduler asks for booking details (bookingform), which nak does not support — book in Moodle")
		case found.Form.What != "bookslot":
			return nil, argErr("slot %d cannot be booked (unexpected form %q)", slotID, found.Form.What)
		}
		return found, nil
	}
	switch {
	case found == nil && unidentified > 0:
		return nil, argErr("Moodle shows your booking without a cancel button (cancelling is no longer allowed)")
	case found == nil || found.Status != "booked":
		return nil, argErr("slot %d is not one of your bookings (yours: %s)", slotID, strings.Join(ids, ", "))
	case found.Form == nil || found.Form.What != "cancelbooking":
		return nil, argErr("Moodle offers no cancel button for slot %d (cancelling is no longer allowed)", slotID)
	}
	return found, nil
}

// checkForm refuses forms that do not do exactly what the tool promises.
func (s *Service) checkForm(f *SlotForm, cmid, slotID int64) error {
	u, err := url.Parse(f.Action)
	if err != nil || !s.C.sameHost(u) {
		return argErr("the page's form points to another host (%s); refusing to send", f.Action)
	}
	if u.Path != schedulerPath {
		return argErr("the page's form points to %s, not %s; refusing to send", u.Path, schedulerPath)
	}
	if f.get("id") != strconv.FormatInt(cmid, 10) || f.get("slotid") != strconv.FormatInt(slotID, 10) || f.get("sesskey") == "" {
		return argErr("the page's form does not match cmid/slotid; refusing to send")
	}
	return nil
}

func visibleFields(f *SlotForm) *M {
	o := m()
	for _, x := range f.Fields {
		if x.Name != "sesskey" {
			o.Put(x.Name, x.Value)
		}
	}
	return o
}

// verifyAct reads the page Moodle redirected to: the action counts only if the
// booking state really changed.
func (s *Service) verifyAct(body string, slotID int64, book bool, action string, details *M) (*M, error) {
	p, err := ParseScheduler(body, s.Zone)
	if err != nil {
		if invalidSesskey.MatchString(body) {
			return nil, sesskeyErr()
		}
		return nil, err
	}
	if len(p.Alerts) > 0 {
		if invalidSesskey.MatchString(strings.Join(p.Alerts, " ")) {
			return nil, sesskeyErr()
		}
		return nil, &Error{Code: "scheduler_rejected", Msg: "Moodle rejected the request: " + strings.Join(p.Alerts, "; ")}
	}
	mine := false
	for _, sl := range p.Slots {
		if sl.SlotID == slotID && sl.Status == "booked" {
			mine = true
		}
	}
	if mine == book {
		return m("mode", "sent", "action", action, "slot", details, "result", map[bool]string{true: "booked", false: "cancelled"}[book]), nil
	}
	return nil, &Error{Code: "scheduler_rejected", Msg: "Moodle did not confirm the change; check moodle_scheduler for the current state"}
}

func sesskeyErr() error {
	return &Error{Code: "invalid_sesskey", Msg: "Moodle rejected the request: invalid session key; nothing was changed, try again"}
}
