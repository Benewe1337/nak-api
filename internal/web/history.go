package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Raindancer118/nak-api/internal/grades"
)

// history remembers what the CIS forgets: an exam drops out of the exam list
// once it is over, and the grades page only shows the latest attempt. Every
// exam list and grade overview that passes through naknak is recorded.
type history struct {
	mu   sync.Mutex
	file string
	now  func() time.Time
	data historyData
}

type historyData struct {
	Exams  map[string]examRecord    `json:"exams"`
	Grades map[string][]gradeRecord `json:"grades"`
}

type examRecord struct {
	ExamID     string    `json:"exam_id"`
	ModuleNr   string    `json:"module_nr"`
	Title      string    `json:"title"`
	Start      string    `json:"start"`
	End        string    `json:"end,omitempty"`
	Registered bool      `json:"registered"`
	Dozenten   []string  `json:"dozenten,omitempty"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	// Manual: entered by the owner ("Klausur geschrieben"), for an exam naknak never saw in the CIS.
	Manual bool `json:"manual,omitempty"`
}

type gradeRecord struct {
	ModuleNr string    `json:"module_nr"`
	Title    string    `json:"title"`
	Grade    string    `json:"grade"`
	Status   string    `json:"status"`
	ExamDate string    `json:"exam_date"`
	Attempt  int       `json:"attempt"`
	Seen     time.Time `json:"seen"`
}

func newHistory(file string, now func() time.Time) *history {
	h := &history{file: file, now: now}
	if file != "" {
		if b, err := os.ReadFile(file); err == nil {
			json.Unmarshal(b, &h.data)
		}
	}
	if h.data.Exams == nil {
		h.data.Exams = map[string]examRecord{}
	}
	if h.data.Grades == nil {
		h.data.Grades = map[string][]gradeRecord{}
	}
	return h
}

func strs(v any) []string {
	arr, _ := v.([]any)
	var out []string
	for _, x := range arr {
		if s := strings.TrimSpace(fmt.Sprint(x)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// observe takes a fresh tool result; unrelated tools are ignored.
func (h *history) observe(tool string, raw json.RawMessage) {
	var exams []map[string]any
	var modules []map[string]any
	switch tool {
	case "cis_list_klausuren":
		json.Unmarshal(raw, &exams)
	case "nak_module":
		var m struct {
			Exams []map[string]any `json:"exams"`
		}
		json.Unmarshal(raw, &m)
		exams = m.Exams
	case "cis_transcript_grades":
		var t struct {
			Modules []map[string]any `json:"modules"`
		}
		json.Unmarshal(raw, &t)
		h.observeTranscript(t.Modules)
		return
	case "cis_grades":
		var g struct {
			Overview struct {
				Modules []map[string]any `json:"modules"`
			} `json:"overview"`
		}
		json.Unmarshal(raw, &g)
		modules = g.Overview.Modules
	default:
		return
	}
	if len(exams) == 0 && len(modules) == 0 {
		return
	}
	now := h.now()
	h.mu.Lock()
	changed := false
	for _, e := range exams {
		id := str(e, "exam_id")
		if id == "" {
			continue
		}
		rec, ok := h.data.Exams[id]
		if !ok {
			rec.FirstSeen = now
		}
		rec.ExamID, rec.ModuleNr, rec.Title, rec.Start = id, str(e, "module_nr"), str(e, "title"), str(e, "start")
		rec.End = str(e, "ende")
		rec.Registered, _ = e["registered"].(bool)
		if d := strs(e["dozenten"]); len(d) > 0 {
			rec.Dozenten = d
		}
		rec.LastSeen = now
		h.data.Exams[id] = rec
		changed = true
	}
	for _, m := range modules {
		nr := strings.SplitN(str(m, "module_nr"), ",", 2)[0]
		grade, date := str(m, "grade"), str(m, "exam_date")
		if nr == "" || (grade == "" && date == "") {
			continue
		}
		attempt := 0
		if f, ok := m["attempt"].(float64); ok {
			attempt = int(f)
		}
		steps := h.data.Grades[nr]
		if n := len(steps); n > 0 && steps[n-1].Grade == grade && steps[n-1].ExamDate == date && steps[n-1].Attempt == attempt {
			continue
		}
		h.data.Grades[nr] = append(steps, gradeRecord{nr, str(m, "title"), grade, str(m, "status"), date, attempt, now})
		changed = true
	}
	var b []byte
	if changed && h.file != "" {
		b, _ = json.Marshal(h.data)
	}
	h.mu.Unlock()
	if b != nil && os.WriteFile(h.file+".tmp", b, 0o600) == nil {
		os.Rename(h.file+".tmp", h.file)
	}
}

// observeTranscript records grades from the PDF: no exam dates there, so a
// step only says "this result was there at Seen"; same grade, no new step.
func (h *history) observeTranscript(mods []map[string]any) {
	now := h.now()
	h.mu.Lock()
	changed := false
	for _, m := range mods {
		nr, grade := str(m, "module_nr"), str(m, "grade")
		if nr == "" || grade == "" {
			continue
		}
		steps := h.data.Grades[nr]
		if n := len(steps); n > 0 && grades.NormalizeGrade(steps[n-1].Grade) == grades.NormalizeGrade(grade) {
			continue
		}
		h.data.Grades[nr] = append(steps, gradeRecord{ModuleNr: nr, Title: str(m, "title"), Grade: grade, Seen: now})
		changed = true
	}
	var b []byte
	if changed && h.file != "" {
		b, _ = json.Marshal(h.data)
	}
	h.mu.Unlock()
	if b != nil && os.WriteFile(h.file+".tmp", b, 0o600) == nil {
		os.Rename(h.file+".tmp", h.file)
	}
}

type historySnapshot struct {
	Exams  []examRecord             `json:"exams"`
	Grades map[string][]gradeRecord `json:"grades"`
}

func (h *history) snapshot() historySnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := historySnapshot{Exams: make([]examRecord, 0, len(h.data.Exams)), Grades: map[string][]gradeRecord{}}
	for _, e := range h.data.Exams {
		out.Exams = append(out.Exams, e)
	}
	sort.Slice(out.Exams, func(i, j int) bool { return out.Exams[i].FirstSeen.Before(out.Exams[j].FirstSeen) })
	for k, v := range h.data.Grades {
		out.Grades[k] = append([]gradeRecord(nil), v...)
	}
	return out
}

// addManual records an exam the owner wrote but naknak never saw (the CIS drops it from the
// exam list once it is over). It is registered by definition.
func (h *history) addManual(nr, title, date string) examRecord {
	now := h.now()
	rec := examRecord{ExamID: "manual-" + nr + "-" + strings.ReplaceAll(date, ".", ""), ModuleNr: nr, Title: title,
		Start: date, Registered: true, FirstSeen: now, LastSeen: now, Manual: true}
	h.mu.Lock()
	h.data.Exams[rec.ExamID] = rec
	b, _ := json.Marshal(h.data)
	h.mu.Unlock()
	h.write(b)
	return rec
}

// removeManual deletes a hand-made entry; what came from the CIS stays.
func (h *history) removeManual(id string) bool {
	h.mu.Lock()
	rec, ok := h.data.Exams[id]
	if !ok || !rec.Manual {
		h.mu.Unlock()
		return false
	}
	delete(h.data.Exams, id)
	b, _ := json.Marshal(h.data)
	h.mu.Unlock()
	h.write(b)
	return true
}

func (h *history) write(b []byte) {
	if h.file != "" && os.WriteFile(h.file+".tmp", b, 0o600) == nil {
		os.Rename(h.file+".tmp", h.file)
	}
}

func (s *Server) addManualExam(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ModuleNr string `json:"module_nr"`
		Title    string `json:"title"`
		Date     string `json:"date"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	nr, title := strings.TrimSpace(in.ModuleNr), strings.TrimSpace(in.Title)
	day, err := time.ParseInLocation("02.01.2006", strings.TrimSpace(in.Date), s.app.Zone)
	now := s.cfg.Now().In(s.app.Zone)
	switch {
	case nr == "" || title == "" || len(nr) > 40 || len(title) > 200:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "module_nr and title are required"})
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "date must be DD.MM.YYYY"})
	case day.After(now) || now.Sub(day) > pendingWindow:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "the exam must be in the past and not older than the grading window"})
	default:
		writeJSON(w, http.StatusOK, s.history.addManual(nr, title, day.Format("02.01.2006")))
	}
}

func (s *Server) removeManualExam(w http.ResponseWriter, r *http.Request) {
	if !s.history.removeManual(r.PathValue("id")) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no hand-made exam with this id"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

func (s *Server) historyAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.history.snapshot())
}

func (h *history) reset() {
	h.mu.Lock()
	h.data = historyData{Exams: map[string]examRecord{}, Grades: map[string][]gradeRecord{}}
	h.mu.Unlock()
	if h.file != "" {
		os.Remove(h.file)
	}
}
