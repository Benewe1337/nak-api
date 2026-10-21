package web

import (
	"encoding/json"
	"time"

	"github.com/Raindancer118/nak-api/internal/grades"
	"github.com/Raindancer118/nak-api/internal/tools"
)

// withTranscript: while a grade is due the watcher reads the Notenübersicht
// PDF, which knows new grades before the Leistungsübersicht. Whatever it found
// is laid over the page's results so the module page shows the grade that was
// just notified. Only the cached PDF is used, never an extra CIS call.
func (s *Server) withTranscript(tool string, args tools.Args, res json.RawMessage, at time.Time) json.RawMessage {
	if !(tool == "cis_grades" && args.Str("status") == "") && tool != "nak_module" {
		return res
	}
	k, _ := json.Marshal(tools.Args{})
	e, ok := s.store.get("cis_transcript_grades " + string(k))
	if !ok {
		return res
	}
	var tg grades.TranscriptGrades
	var out map[string]json.RawMessage
	if json.Unmarshal(e.Res, &tg) != nil || len(tg.Modules) == 0 || json.Unmarshal(res, &out) != nil {
		return res
	}
	newer := e.At.After(at)
	switch tool {
	case "cis_grades":
		var o grades.Overview
		if json.Unmarshal(out["overview"], &o) != nil || len(o.ApplyTranscript(&tg, newer)) == 0 {
			return res
		}
		out["overview"], _ = json.Marshal(o)
		out["stats"], _ = json.Marshal(o.Stats())
	case "nak_module":
		var nr string
		json.Unmarshal(out["module_nr"], &nr)
		var o grades.Overview
		var m grades.ModuleGrade
		if json.Unmarshal(out["grade"], &m) == nil && m.ModuleNr != "" {
			o.Modules = []grades.ModuleGrade{m}
		}
		mine := grades.TranscriptGrades{Date: tg.Date}
		for _, g := range tg.Modules {
			if grades.SameModule(nr, g.ModuleNr) || (m.ModuleNr != "" && grades.SameModule(m.ModuleNr, g.ModuleNr)) {
				mine.Modules = append(mine.Modules, g)
			}
		}
		if len(o.ApplyTranscript(&mine, newer)) == 0 {
			return res
		}
		out["grade"], _ = json.Marshal(o.Modules[0])
		if _, ok := out["title"]; !ok {
			out["title"], _ = json.Marshal(o.Modules[0].Title)
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return res
	}
	return b
}
