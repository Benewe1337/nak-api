package grades

import (
	"regexp"
	"strings"

	"github.com/Raindancer118/nak-api/internal/drift"
)

// The Notenübersicht PDF is generated on request and knows a new grade before
// the Leistungsübersicht page shows it, sometimes by twenty minutes.

type TranscriptGrade struct {
	ModuleNr string `json:"module_nr"`
	Title    string `json:"title"`
	Grade    string `json:"grade"`
	Credits  string `json:"credits,omitempty"`
}

type TranscriptGrades struct {
	Modules []TranscriptGrade `json:"modules"`
	Average string            `json:"average,omitempty"`
	Date    string            `json:"date,omitempty"` // as printed: Datum der Ausstellung
}

var (
	rowRe     = regexp.MustCompile(`^([A-Z]{1,3}\d{3})\s+(.*)$`)
	colsRe    = regexp.MustCompile(`\s{2,}`)
	creditsRe = regexp.MustCompile(`^(\d+(?:,\d+)?) CP$`)
	averageRe = regexp.MustCompile(`Notendurchschnitt ist\s+(\d,\d+)`)
	issuedRe  = regexp.MustCompile(`Ausstellung der Notenübersicht:\s*(\d{2}\.\d{2}\.\d{4})`)
)

// ParseTranscriptText reads the text of the Notenübersicht (pdftotext -layout):
// one row per module with title, grade and credits; "#" marks modules
// without an exam yet, long titles wrap with the grade on the next line.
func ParseTranscriptText(text string) (*TranscriptGrades, error) {
	if !strings.Contains(text, "ModulNr") {
		return nil, drift.New("Notenübersicht (PDF)", "table header ModulNr / Name / Note / Credits", text)
	}
	out := &TranscriptGrades{Modules: []TranscriptGrade{}}
	var open *TranscriptGrade // row whose grade is still to come
	for _, line := range strings.Split(text, "\n") {
		if m := rowRe.FindStringSubmatch(strings.TrimRight(line, " ")); m != nil {
			g := TranscriptGrade{ModuleNr: m[1]}
			done := fill(&g, colsRe.Split(strings.TrimSpace(m[2]), -1))
			if done {
				out.add(g)
				open = nil
			} else {
				open = &g
			}
			continue
		}
		if open != nil && strings.HasPrefix(line, " ") && strings.TrimSpace(line) != "" {
			cols := colsRe.Split(strings.TrimSpace(line), -1)
			cols[0] = open.Title + " " + cols[0]
			if fill(open, cols) {
				out.add(*open)
			}
			open = nil
			continue
		}
		open = nil
	}
	if m := averageRe.FindStringSubmatch(text); m != nil {
		out.Average = m[1]
	}
	if m := issuedRe.FindStringSubmatch(text); m != nil {
		out.Date = m[1]
	}
	return out, nil
}

// fill takes [title, grade?, credits?]; false means the grade column is
// missing (the title wraps onto the next line).
func fill(g *TranscriptGrade, cols []string) bool {
	g.Title = strings.TrimSpace(cols[0])
	if len(cols) < 2 {
		return false
	}
	g.Grade = strings.TrimSpace(cols[1])
	if len(cols) > 2 {
		if m := creditsRe.FindStringSubmatch(strings.TrimSpace(cols[2])); m != nil {
			g.Credits = m[1]
		}
	}
	return true
}

func (t *TranscriptGrades) add(g TranscriptGrade) {
	if g.Grade == "" || g.Grade == "#" {
		return
	}
	t.Modules = append(t.Modules, g)
}

var spaceRe = regexp.MustCompile(`\s+`)

// NormalizeGrade makes the PDF's "4,0 (2. Versuch)" and the page's
// "4,0 (2.Versuch)" the same value; markers like "*" (recognised) go.
func NormalizeGrade(g string) string {
	g = strings.ToLower(spaceRe.ReplaceAllString(g, ""))
	return strings.TrimRight(g, "*⁂")
}

const SourceTranscript = "notenuebersicht"

// ApplyTranscript fills in grades the page does not show yet; a grade the page
// shows differently is only replaced if the PDF is the newer of the two (the
// page lags behind, so a fresher page without the grade proves nothing).
// Returns the module numbers changed.
func (o *Overview) ApplyTranscript(t *TranscriptGrades, pdfNewer bool) []string {
	var changed []string
	for _, tg := range t.Modules {
		m := o.findAny(tg.ModuleNr)
		if m != nil && (NormalizeGrade(m.Grade) == NormalizeGrade(tg.Grade) || strings.TrimSpace(m.Grade) != "" && !pdfNewer) {
			continue
		}
		if m == nil {
			o.Modules = append(o.Modules, ModuleGrade{ModuleNr: tg.ModuleNr, Title: tg.Title})
			m = &o.Modules[len(o.Modules)-1]
		}
		if strings.TrimSpace(m.Grade) != "" {
			// the dates belong to the result being replaced
			m.ExamDate = ""
		}
		m.Grade = tg.Grade
		m.GradeValue, m.Attempt, m.Status = parseGrade(tg.Grade)
		m.Recognized = strings.Contains(tg.Grade, "*")
		m.EntryDate = t.Date
		if m.Credits == "" {
			m.Credits = tg.Credits
		}
		m.Source = SourceTranscript
		changed = append(changed, tg.ModuleNr)
	}
	return changed
}

// findAny also matches rows listing several numbers ("A222,I222").
func (o *Overview) findAny(nr string) *ModuleGrade {
	for _, list := range []*[]ModuleGrade{&o.Modules, &o.Extra} {
		for i := range *list {
			if SameModule((*list)[i].ModuleNr, nr) {
				return &(*list)[i]
			}
		}
	}
	return nil
}

// SameModule reports whether a comma-separated module number list contains nr.
func SameModule(list, nr string) bool {
	for _, x := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(nr)) {
			return true
		}
	}
	return false
}
