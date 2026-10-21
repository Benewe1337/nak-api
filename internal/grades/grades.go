// Package grades reads the Leistungsübersicht (tx_nagrades): module grades,
// seminars, Transferleistungen, per-exam grade distributions and attendance.
//
// The page renders four Bootstrap tabs (#curricular, #extra, #seminar,
// #report). Cells carry data-label attributes, which are used instead of
// column positions so layout changes do not shift values.
package grades

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/Raindancer118/nak-api/internal/client"
	"github.com/Raindancer118/nak-api/internal/drift"
	"github.com/Raindancer118/nak-api/internal/htmlx"
	"golang.org/x/net/html"
)

const PagePath = "/mein-profil/mein-postfach/leistungsuebersicht"

const (
	StatusPassed = "bestanden"
	StatusFailed = "nicht bestanden"
	StatusOpen   = "offen"
)

type ModuleGrade struct {
	ModuleNr      string   `json:"module_nr"`
	Title         string   `json:"title"`
	ExamDate      string   `json:"exam_date,omitempty"`
	EntryDate     string   `json:"entry_date,omitempty"`
	Grade         string   `json:"grade"` // as shown, e.g. "5,0 (2.Versuch)"
	GradeValue    *float64 `json:"grade_value,omitempty"`
	Attempt       int      `json:"attempt,omitempty"`
	Status        string   `json:"status"`
	Credits       string   `json:"credits,omitempty"`
	Recognized    bool     `json:"recognized,omitempty"`      // "*" — result recognised (anerkannt)
	OralAllowed   bool     `json:"oral_supplement,omitempty"` // "(m)" — mündliche Ergänzungsprüfung zulässig
	PerformanceID string   `json:"performance_id,omitempty"`
	StatisticURL  string   `json:"statistic_url,omitempty"`
	AttendanceURL string   `json:"attendance_url,omitempty"`
	// Source is SourceTranscript when the grade comes from the Notenübersicht
	// PDF and the page does not show it yet.
	Source string `json:"source,omitempty"`
}

type SeminarGrade struct {
	Title      string `json:"title"`
	Period     string `json:"period"`
	Grade      string `json:"grade"`
	Credits    string `json:"credits"`
	Recognized bool   `json:"recognized,omitempty"`
}

type TransferGrade struct {
	No      string `json:"no"`
	Title   string `json:"title"`
	Module  string `json:"module"`
	Date    string `json:"date"`
	Grade   string `json:"grade"`
	Status  string `json:"status"`
	Credits string `json:"credits"`
}

type Transcript struct {
	Lang string `json:"lang"`
	URL  string `json:"url"`
}

type Overview struct {
	Student         string          `json:"student"`
	Modules         []ModuleGrade   `json:"modules"`
	Extra           []ModuleGrade   `json:"extra,omitempty"`
	Average         string          `json:"average"` // Durchschnittsnote as published by the CIS
	CreditsTotal    string          `json:"credits_total"`
	Seminars        []SeminarGrade  `json:"seminars"`
	SeminarCredits  string          `json:"seminar_credits,omitempty"`
	Transfers       []TransferGrade `json:"transfers"`
	TransferCredits string          `json:"transfer_credits,omitempty"`
	Transcripts     []Transcript    `json:"transcripts"`
	Footnotes       string          `json:"footnotes,omitempty"`
}

func Fetch(c *client.Client) (*Overview, error) {
	p, err := c.Page(PagePath)
	if err != nil {
		return nil, fmt.Errorf("leistungsübersicht: %w", err)
	}
	o := ParseOverview(p.Body, c.Base)
	if len(o.Modules) == 0 && len(o.Seminars) == 0 {
		return nil, drift.New(PagePath, "grade tables in tabs #curricular/#seminar with data-label cells", p.Body)
	}
	return o, nil
}

func ParseOverview(body, base string) *Overview {
	doc := htmlx.MustParse(body)
	o := &Overview{}
	if h := htmlx.First(doc, func(n *html.Node) bool {
		return htmlx.Tag("h2")(n) && strings.HasPrefix(htmlx.Text(n), "Leistungsübersicht ")
	}); h != nil {
		o.Student = strings.TrimPrefix(htmlx.Text(h), "Leistungsübersicht ")
	}

	if pane := htmlx.First(doc, htmlx.ID("curricular")); pane != nil {
		o.Modules, o.Average, o.CreditsTotal = parseModuleTable(pane, base)
	}
	if pane := htmlx.First(doc, htmlx.ID("extra")); pane != nil {
		o.Extra, _, _ = parseModuleTable(pane, base)
	}
	if pane := htmlx.First(doc, htmlx.ID("seminar")); pane != nil {
		for _, tr := range htmlx.Rows(pane) {
			cells := labelled(tr)
			if cells["Bezeichnung"] == "" {
				if v := lastNonEmpty(tr); v != "" {
					o.SeminarCredits = v
				}
				continue
			}
			g := cells["Note"]
			o.Seminars = append(o.Seminars, SeminarGrade{Title: cells["Bezeichnung"], Period: cells["Zeitraum"],
				Grade: g, Credits: cells["Credits"], Recognized: strings.Contains(g, "*")})
		}
	}
	if pane := htmlx.First(doc, htmlx.ID("report")); pane != nil {
		for _, tr := range htmlx.Rows(pane) {
			cells := labelled(tr)
			if cells["Bezeichnung"] == "" {
				if v := lastNonEmpty(tr); v != "" {
					o.TransferCredits, o.Footnotes = splitLead(v)
				}
				continue
			}
			o.Transfers = append(o.Transfers, TransferGrade{No: cells["Nummer"], Title: cells["Bezeichnung"],
				Module: cells["Modul"], Date: cells["Datum"], Grade: cells["Note"], Status: cells["Status"], Credits: cells["Credits"]})
		}
	}
	for _, a := range htmlx.All(doc, htmlx.HrefContains("[action]=transcript")) {
		href := htmlx.AbsURL(base, htmlx.Attr(a, "href"))
		o.Transcripts = append(o.Transcripts, Transcript{Lang: htmlx.QueryParam(href, "lang"), URL: href})
	}
	return o
}

func parseModuleTable(pane *html.Node, base string) (mods []ModuleGrade, avg, credits string) {
	for _, tr := range htmlx.Rows(pane) {
		cells := labelled(tr)
		if v, ok := cells["Durchschnittsnote"]; ok {
			avg, credits = v, cells["Credits gesamt"]
			continue
		}
		if cells["Modulnummer"] == "" && cells["Bezeichnung"] == "" {
			continue
		}
		raw := cells["Note"]
		v, attempt, status := parseGrade(raw)
		m := ModuleGrade{ModuleNr: cells["Modulnummer"], Title: cells["Bezeichnung"], ExamDate: cells["Prüfungsdatum"],
			EntryDate: cells["Noteneingabedatum"], Grade: raw, GradeValue: v, Attempt: attempt, Status: status,
			Credits: cells["Credits"], Recognized: strings.Contains(raw, "*"), OralAllowed: strings.Contains(raw, "(m)")}
		for _, a := range htmlx.All(tr, htmlx.Tag("a")) {
			href := htmlx.AbsURL(base, htmlx.Attr(a, "href"))
			switch htmlx.Action(href) {
			case "statistic":
				m.StatisticURL = href
				m.PerformanceID = htmlx.QueryParam(href, "performanceId")
			case "anwesenheiten":
				m.AttendanceURL = href
			}
		}
		mods = append(mods, m)
	}
	return mods, avg, credits
}

// labelled maps each cell's data-label (without colon) to its text.
func labelled(tr *html.Node) map[string]string {
	out := map[string]string{}
	for _, td := range htmlx.Cells(tr) {
		if l := strings.TrimSuffix(strings.TrimSpace(htmlx.Attr(td, "data-label")), ":"); l != "" {
			out[l] = htmlx.Text(td)
		}
	}
	return out
}

func lastNonEmpty(tr *html.Node) string {
	cells := htmlx.Cells(tr)
	for i := len(cells) - 1; i >= 0; i-- {
		if t := htmlx.Text(cells[i]); t != "" {
			return t
		}
	}
	return ""
}

// splitLead splits "20 (m) eine mündliche…" into "20" and the footnote text.
func splitLead(s string) (string, string) {
	if i := strings.IndexByte(s, ' '); i > 0 {
		if _, err := strconv.Atoi(s[:i]); err == nil {
			return s[:i], strings.TrimSpace(s[i:])
		}
	}
	return s, ""
}

var (
	gradeNum = regexp.MustCompile(`^(\d)[,.](\d)`)
	attemptR = regexp.MustCompile(`\((\d+)\.\s*Versuch\)`)
)

// parseGrade interprets a grade cell. German grades ≤ 4,0 pass.
func parseGrade(raw string) (*float64, int, string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, 0, StatusOpen
	}
	attempt := 1
	if m := attemptR.FindStringSubmatch(s); m != nil {
		attempt, _ = strconv.Atoi(m[1])
	}
	if m := gradeNum.FindStringSubmatch(s); m != nil {
		v, _ := strconv.ParseFloat(m[1]+"."+m[2], 64)
		status := StatusPassed
		if v > 4.0 {
			status = StatusFailed
		}
		return &v, attempt, status
	}
	low := strings.ToLower(s)
	if strings.Contains(low, "nicht") || strings.Contains(low, "n.b") {
		return nil, attempt, StatusFailed
	}
	if strings.HasPrefix(low, "best") || strings.Contains(low, "passed") {
		return nil, attempt, StatusPassed
	}
	return nil, attempt, StatusOpen
}

// Stats summarises module progress. WeightedAverage is a credit-weighted mean
// over graded, passed modules — a cross-check for the CIS Durchschnittsnote.
type Stats struct {
	Passed          int     `json:"passed"`
	Failed          int     `json:"failed"`
	Open            int     `json:"open"`
	CreditsEarned   float64 `json:"credits_earned"`
	WeightedAverage float64 `json:"weighted_average"`
	BestGrade       string  `json:"best_grade,omitempty"`
	WorstGrade      string  `json:"worst_passed_grade,omitempty"`
}

func (o *Overview) Stats() Stats {
	var s Stats
	var sum, w float64
	best, worst := math.Inf(1), math.Inf(-1)
	for _, m := range o.Modules {
		switch m.Status {
		case StatusPassed:
			s.Passed++
			cr := parseNum(m.Credits)
			s.CreditsEarned += cr
			if m.GradeValue != nil && cr > 0 {
				sum += *m.GradeValue * cr
				w += cr
				best = math.Min(best, *m.GradeValue)
				worst = math.Max(worst, *m.GradeValue)
			}
		case StatusFailed:
			s.Failed++
		default:
			s.Open++
		}
	}
	if w > 0 {
		s.WeightedAverage = math.Round(sum/w*100) / 100
		s.BestGrade = fmtGrade(best)
		s.WorstGrade = fmtGrade(worst)
	}
	return s
}

func fmtGrade(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 1, 64), ".", ",", 1)
}

func parseNum(s string) float64 {
	f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64)
	if err != nil {
		return 0
	}
	return f
}

// Find returns the module with the given number (case-insensitive).
func (o *Overview) Find(moduleNr string) *ModuleGrade {
	for i := range o.Modules {
		if strings.EqualFold(o.Modules[i].ModuleNr, moduleNr) {
			return &o.Modules[i]
		}
	}
	for i := range o.Extra {
		if strings.EqualFold(o.Extra[i].ModuleNr, moduleNr) {
			return &o.Extra[i]
		}
	}
	return nil
}

// ── grade distribution (action=statistic) ───────────────────────────────────

type Bucket struct {
	Grade string `json:"grade"`
	Count int    `json:"count"`
}

type Distribution struct {
	Module    string   `json:"module"`
	Lecturers []string `json:"lecturers"`
	Average   float64  `json:"average"`
	Count     int      `json:"count"`
	Buckets   []Bucket `json:"buckets"`
	FailRate  float64  `json:"fail_rate_percent"`
	// Filled by Place when the student's own grade is known.
	YourGrade  string  `json:"your_grade,omitempty"`
	Better     int     `json:"better_than_you,omitempty"`
	Same       int     `json:"same_as_you,omitempty"`
	Worse      int     `json:"worse_than_you,omitempty"`
	Percentile float64 `json:"percentile,omitempty"` // share of results you are at least as good as
}

var (
	distHead  = regexp.MustCompile(`Notenverteilung im Modul (.+?) bei (.+)$`)
	distAvg   = regexp.MustCompile(`Durchschnitt:\s*([\d,.]+)\s*-\s*(\d+)`)
	distPairs = regexp.MustCompile(`\['(\d[,.]\d)',\s*(\d+)\]`)
)

func ParseDistribution(body string) (*Distribution, error) {
	doc := htmlx.MustParse(body)
	d := &Distribution{}
	box := htmlx.First(doc, htmlx.Class("tx_na_grades"))
	if box == nil {
		return nil, drift.New(PagePath+" (statistic)", "content box .tx_na_grades with chart data", body)
	}
	if h := htmlx.First(box, htmlx.Tag("h3")); h != nil {
		if m := distHead.FindStringSubmatch(htmlx.Text(h)); m != nil {
			d.Module = m[1]
			for _, l := range strings.Split(m[2], ",") {
				if l = strings.TrimSpace(l); l != "" {
					d.Lecturers = append(d.Lecturers, l)
				}
			}
		}
	}
	if m := distAvg.FindStringSubmatch(htmlx.Text(box)); m != nil {
		d.Average = parseNum(m[1])
		d.Count, _ = strconv.Atoi(m[2])
	}
	// The data lives in the Google Charts init script: [['Note','Anzahl'],['1,0',1],…]
	for _, m := range distPairs.FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(m[2])
		if len(d.Buckets) > 0 && d.Buckets[0].Grade == m[1] {
			break // second chart repeats the data
		}
		d.Buckets = append(d.Buckets, Bucket{Grade: m[1], Count: n})
	}
	failed := 0
	for _, b := range d.Buckets {
		if parseNum(b.Grade) > 4.0 {
			failed += b.Count
		}
	}
	if d.Count > 0 {
		d.FailRate = math.Round(float64(failed)/float64(d.Count)*1000) / 10
	}
	return d, nil
}

// Place positions a grade inside the distribution.
func (d *Distribution) Place(grade float64) {
	d.YourGrade = fmtGrade(grade)
	d.Better, d.Same, d.Worse = 0, 0, 0
	for _, b := range d.Buckets {
		g := parseNum(b.Grade)
		switch {
		case math.Abs(g-grade) < 0.01:
			d.Same += b.Count
		case g < grade:
			d.Better += b.Count
		default:
			d.Worse += b.Count
		}
	}
	if d.Count > 0 {
		d.Percentile = math.Round(float64(d.Worse+d.Same)/float64(d.Count)*1000) / 10
	}
}

// FetchDistribution loads the distribution for a module number and places the
// student's own grade in it.
func FetchDistribution(c *client.Client, moduleNr string) (*Distribution, error) {
	o, err := Fetch(c)
	if err != nil {
		return nil, err
	}
	m := o.Find(moduleNr)
	if m == nil {
		return nil, fmt.Errorf("module %s not in the Leistungsübersicht", moduleNr)
	}
	if m.StatisticURL == "" {
		return nil, fmt.Errorf("module %s has no grade distribution yet (no graded exam)", moduleNr)
	}
	p, err := c.Page(m.StatisticURL)
	if err != nil {
		return nil, err
	}
	d, err := ParseDistribution(p.Body)
	if err != nil {
		return nil, err
	}
	if m.GradeValue != nil {
		d.Place(*m.GradeValue)
	}
	return d, nil
}

// ── attendance (action=anwesenheiten) ───────────────────────────────────────

type Session struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Status string `json:"status"`
}

type Attendance struct {
	Module         string         `json:"module"`
	ModuleNr       string         `json:"module_nr"`
	Period         string         `json:"period,omitempty"`
	Sessions       []Session      `json:"sessions"`
	Summary        map[string]int `json:"summary"`
	AttendanceRate float64        `json:"attendance_rate_percent"` // attended / sessions with a recorded status
}

var attHead = regexp.MustCompile(`im Modul (.+) \(([^()]+)\)\s*$`)

func ParseAttendance(body string) *Attendance {
	doc := htmlx.Main(htmlx.MustParse(body))
	a := &Attendance{Summary: map[string]int{}}
	for _, h := range htmlx.All(doc, htmlx.Tag("h2", "h3", "h4")) {
		t := htmlx.Text(h)
		if m := attHead.FindStringSubmatch(t); m != nil && strings.HasPrefix(t, "Anwesenheiten") {
			a.Module, a.ModuleNr = m[1], m[2]
		}
		if rest, ok := strings.CutPrefix(t, "Veranstaltung vom "); ok {
			a.Period = rest
		}
	}
	for _, tbl := range htmlx.All(doc, htmlx.Tag("table")) {
		for _, tr := range htmlx.Rows(tbl) {
			cells := htmlx.Cells(tr)
			if len(cells) < 3 {
				continue
			}
			s := Session{From: htmlx.Text(cells[0]), To: htmlx.Text(cells[1]), Status: htmlx.Text(cells[2])}
			a.Sessions = append(a.Sessions, s)
			a.Summary[s.Status]++
		}
	}
	recorded, attended := 0, 0
	for st, n := range a.Summary {
		if strings.Contains(st, "offen") {
			continue
		}
		recorded += n
		if strings.HasPrefix(st, "Teilgenommen") {
			attended += n
		}
	}
	if recorded > 0 {
		a.AttendanceRate = math.Round(float64(attended)/float64(recorded)*1000) / 10
	}
	return a
}

func FetchAttendance(c *client.Client, moduleNr string) (*Attendance, error) {
	o, err := Fetch(c)
	if err != nil {
		return nil, err
	}
	m := o.Find(moduleNr)
	if m == nil || m.AttendanceURL == "" {
		return nil, fmt.Errorf("no attendance link for module %s", moduleNr)
	}
	p, err := c.Page(m.AttendanceURL)
	if err != nil {
		return nil, err
	}
	return ParseAttendance(p.Body), nil
}

// FetchTranscript downloads the Notenspiegel PDF ("de" or "en").
func FetchTranscript(c *client.Client, lang string) ([]byte, string, error) {
	o, err := Fetch(c)
	if err != nil {
		return nil, "", err
	}
	for _, t := range o.Transcripts {
		if t.Lang == lang {
			data, ct, _, err := c.Download(t.URL)
			return data, ct, err
		}
	}
	return nil, "", fmt.Errorf("no %q transcript link on the Leistungsübersicht", lang)
}
