package web

import (
	"encoding/json"

	"github.com/Raindancer118/nak-api/internal/grades"
	"github.com/Raindancer118/nak-api/internal/tools"
)

// StatusPending marks a module whose next attempt is written but not graded yet.
const StatusPending = "ausstehend"

// withPendingAttempts: while an attempt is written but not graded, the module shows no grade —
// not the result of an earlier attempt (Tom, 28.10.2026). Counted as open in the statistics.
func (s *Server) withPendingAttempts(tool string, args tools.Args, res json.RawMessage) json.RawMessage {
	if !(tool == "cis_grades" && args.Str("status") == "") && tool != "nak_module" {
		return res
	}
	pending := s.pendingExams(s.cfg.Now().In(s.app.Zone))
	if len(pending) == 0 {
		return res
	}
	var out map[string]json.RawMessage
	if json.Unmarshal(res, &out) != nil {
		return res
	}
	hide := func(m *grades.ModuleGrade) bool {
		for _, p := range pending {
			if grades.SameModule(p.ModuleNr, m.ModuleNr) || titleKey(p.Title) == titleKey(m.Title) {
				if m.Attempt > 0 && m.Status == grades.StatusFailed {
					m.Attempt++
				}
				m.Grade, m.GradeValue, m.Status, m.Source = "", nil, StatusPending, ""
				return true
			}
		}
		return false
	}
	switch tool {
	case "cis_grades":
		var o grades.Overview
		if json.Unmarshal(out["overview"], &o) != nil {
			return res
		}
		changed := false
		for i := range o.Modules {
			changed = hide(&o.Modules[i]) || changed
		}
		if !changed {
			return res
		}
		out["overview"], _ = json.Marshal(o)
		counted := o
		counted.Modules = append([]grades.ModuleGrade(nil), o.Modules...)
		for i := range counted.Modules {
			if counted.Modules[i].Status == StatusPending {
				counted.Modules[i].Status = grades.StatusOpen
			}
		}
		out["stats"], _ = json.Marshal(counted.Stats())
	case "nak_module":
		var m grades.ModuleGrade
		if json.Unmarshal(out["grade"], &m) != nil || m.ModuleNr == "" || !hide(&m) {
			return res
		}
		out["grade"], _ = json.Marshal(m)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return res
	}
	return b
}
