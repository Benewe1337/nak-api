// Package health checks, read-only, whether the live CIS still has the
// structure nak's parsers and write flows rely on, and reports drift as
// GitHub issues that contain page outlines only — never personal data.
package health

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Raindancer118/nak-api/internal/auth"
	"github.com/Raindancer118/nak-api/internal/certs"
	"github.com/Raindancer118/nak-api/internal/client"
	"github.com/Raindancer118/nak-api/internal/drift"
	"github.com/Raindancer118/nak-api/internal/exams"
	"github.com/Raindancer118/nak-api/internal/grades"
	"github.com/Raindancer118/nak-api/internal/planning"
	"github.com/Raindancer118/nak-api/internal/profile"
	"github.com/Raindancer118/nak-api/internal/seminars"
	"github.com/Raindancer118/nak-api/internal/stundenplan"
	"github.com/Raindancer118/nak-api/internal/timetable"
	"github.com/Raindancer118/nak-api/internal/transfer"
	"github.com/Raindancer118/nak-api/internal/wahlpflicht"
)

const (
	StatusOK          = "ok"
	StatusDrift       = "drift"       // structure changed → worth an issue
	StatusUnavailable = "unavailable" // network, login, maintenance → not an issue
)

type Result struct {
	Check    string       `json:"check"`
	Status   string       `json:"status"`
	Detail   string       `json:"detail,omitempty"`
	Page     string       `json:"page,omitempty"`
	Expected string       `json:"expected,omitempty"`
	Print    *drift.Print `json:"fingerprint,omitempty"`
}

type Report struct {
	Version string    `json:"version"`
	At      time.Time `json:"at"`
	Results []Result  `json:"results"`
}

func (r *Report) Drifted() []Result {
	var out []Result
	for _, x := range r.Results {
		if x.Status == StatusDrift {
			out = append(out, x)
		}
	}
	return out
}

type check struct {
	name string
	run  func(c *client.Client) error
}

func need(cond bool, page, expected, body string) error {
	if cond {
		return nil
	}
	return drift.New(page, expected, body)
}

func page(c *client.Client, path string) (string, error) {
	p, err := c.Page(path)
	if err != nil {
		return "", err
	}
	return p.Body, nil
}

var checks = []check{
	{"personal data", func(c *client.Client) error { _, err := profile.FetchPersonal(c); return err }},
	{"grades overview", func(c *client.Client) error {
		body, err := page(c, grades.PagePath)
		if err != nil {
			return err
		}
		o := grades.ParseOverview(body, c.Base)
		return need(len(o.Modules) > 0 && len(o.Transcripts) > 0, grades.PagePath, "modules with data-label cells and transcript links", body)
	}},
	{"grade distribution", func(c *client.Client) error {
		o, err := grades.Fetch(c)
		if err != nil {
			return err
		}
		for _, m := range o.Modules {
			if m.StatisticURL == "" {
				continue
			}
			p, err := c.Page(m.StatisticURL)
			if err != nil {
				return err
			}
			d, err := grades.ParseDistribution(p.Body)
			if err != nil {
				return err
			}
			return need(len(d.Buckets) > 0 && d.Count > 0, grades.PagePath+" (statistic)", "chart data ['Note', count] and 'Durchschnitt: x - n'", p.Body)
		}
		return nil
	}},
	{"attendance", func(c *client.Client) error {
		o, err := grades.Fetch(c)
		if err != nil {
			return err
		}
		for _, m := range o.Modules {
			if m.AttendanceURL == "" {
				continue
			}
			p, err := c.Page(m.AttendanceURL)
			if err != nil {
				return err
			}
			a := grades.ParseAttendance(p.Body)
			return need(a.ModuleNr != "", grades.PagePath+" (anwesenheiten)", "heading 'Anwesenheiten … im Modul X (NR)'", p.Body)
		}
		return nil
	}},
	{"exam overview", func(c *client.Client) error { _, err := exams.FetchList(c); return err }},
	{"study plan", func(c *client.Client) error { _, err := planning.FetchStudienplan(c); return err }},
	{"lecture periods", func(c *client.Client) error {
		body, err := page(c, planning.VorlesungszeitenPath)
		if err != nil {
			return err
		}
		return need(len(planning.ParseVorlesungszeiten(body)) > 0, planning.VorlesungszeitenPath, "table of quarters with date ranges", body)
	}},
	{"timetable files", func(c *client.Client) error {
		body, err := page(c, stundenplan.PagePath)
		if err != nil {
			return err
		}
		plans, err := stundenplan.FetchList(c)
		if err != nil {
			return err
		}
		if err := need(len(plans) > 0, stundenplan.PagePath, "ul.ce-uploads with .ics/.html files", body); err != nil {
			return err
		}
		p, err := profile.FetchPersonal(c)
		if err != nil {
			return err
		}
		evs, _, err := timetable.Fetch(c, p.Zenturie)
		if err != nil {
			return err
		}
		return need(len(evs) > 0, stundenplan.PagePath+" (.ics)", "VEVENTs with DESCRIPTION 'Veranstaltung: …'", "")
	}},
	{"seminars", func(c *client.Client) error { _, err := seminars.Fetch(c, "", false); return err }},
	{"electives", func(c *client.Client) error {
		body, err := page(c, wahlpflicht.PagePath)
		if err != nil {
			return err
		}
		l := wahlpflicht.Parse(body)
		return need(len(l.Chosen)+len(l.Available) > 0 || l.Notice != "", wahlpflicht.PagePath, "h3 'gewählte Module'/'alle wählbaren Module' with show forms", body)
	}},
	{"transfer list", func(c *client.Client) error {
		body, err := page(c, transfer.PagePath)
		if err != nil {
			return err
		}
		reps, err := transfer.FetchList(c)
		if err != nil {
			return err
		}
		return need(len(reps) > 0 || strings.Contains(body, "action%5D=new"), transfer.PagePath, "table with Thema/Korrekturfristende or a 'new' link", body)
	}},
	{"certificates", func(c *client.Client) error {
		body, err := page(c, certs.PagePath)
		if err != nil {
			return err
		}
		return need(len(certs.Parse(body, c.Base)) > 0, certs.PagePath, "rows with action=document links", body)
	}},
	{"contact forms", func(c *client.Client) error { _, err := profile.FetchContact(c); return err }},
	{"address form", func(c *client.Client) error { _, err := profile.FetchAddress(c, ""); return err }},
	{"sharing forms", func(c *client.Client) error {
		if _, err := profile.FetchFreigabe(c); err != nil {
			return err
		}
		_, err := profile.FetchDatenfreigabe(c)
		return err
	}},
	{"payment form", func(c *client.Client) error { _, err := profile.FetchPayment(c); return err }},
	{"classmates", func(c *client.Client) error {
		body, err := page(c, profile.KommilitonenPath)
		if err != nil {
			return err
		}
		cm := profile.ParseClassmates(body, c.Base)
		return need(len(cm.People) > 0 && len(cm.Zenturien) > 0, profile.KommilitonenPath, "Zenturie select and a person table", body)
	}},
}

// Names lists the checks in order.
func Names() []string {
	out := make([]string, len(checks))
	for i, c := range checks {
		out[i] = c.name
	}
	return out
}

// Run executes every check (read-only) and classifies the outcome.
func Run(c *client.Client, version string, now time.Time) *Report {
	r := &Report{Version: version, At: now}
	for _, ch := range checks {
		res := Result{Check: ch.name, Status: StatusOK}
		if err := safely(ch.run, c); err != nil {
			res.Status = classify(err)
			var d *drift.Error
			if errors.As(err, &d) {
				res.Page, res.Expected, res.Print = d.Page, d.Expected, d.Fingerprint
			} else if res.Status == StatusDrift {
				res.Page, res.Expected = "/", "login with the stored credentials accepted (login format changed? password?)"
			}
			res.Detail = err.Error()
		}
		r.Results = append(r.Results, res)
	}
	return r
}

// classify: changed structure and a rejected login are drift (worth an issue); everything else
// (network, maintenance, timeouts) is an outage.
func classify(err error) string {
	var d *drift.Error
	if errors.As(err, &d) || errors.Is(err, auth.ErrLoginRejected) {
		return StatusDrift
	}
	return StatusUnavailable
}

func safely(f func(*client.Client) error, c *client.Client) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = drift.New("?", fmt.Sprintf("no panic (parser crashed: %v)", p), "")
		}
	}()
	return f(c)
}
