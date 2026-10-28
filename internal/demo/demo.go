// Package demo is a complete, made-up student for `nak serve --demo`: every
// tool the web UI uses answers with invented data relative to today, so
// naknak can be tried (and screenshotted) without a NAK account. Nothing in
// here talks to the CIS, Moodle or EduVault.
package demo

import (
	"fmt"
	"github.com/Raindancer118/nak-api/internal/mensa"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Raindancer118/nak-api/internal/app"
	"github.com/Raindancer118/nak-api/internal/thesis"
	"github.com/Raindancer118/nak-api/internal/tools"
	"github.com/Raindancer118/nak-api/internal/transfer"
)

type module struct {
	nr, title, grade, status, examDate, lecturer string
	value                                        float64
	attempt, credits, courseID                   int
}

var modules = []module{
	{"I151", "Softwaretechnik", "", "offen", "", "Dr. Ada Lindqvist", 0, 0, 6, 2101},
	{"I160", "Datenbanksysteme", "", "offen", "", "Prof. Jonas Brandt", 0, 0, 6, 2102},
	{"I168", "Diskrete Mathematik 2", "4,0 (2.Versuch)", "bestanden", "11.02.2026", "Prof. Mira Okafor", 4.0, 2, 5, 2103},
	{"I145", "Diskrete Mathematik 1", "2,3", "bestanden", "14.07.2025", "Prof. Mira Okafor", 2.3, 1, 5, 2004},
	{"I201", "Einführung in die objektorientierte Programmierung", "1,3", "bestanden", "03.02.2025", "Felix Sorensen", 1.3, 1, 6, 2005},
	{"I154", "Rechnernetze", "1,7", "bestanden", "22.07.2025", "Dr. Ilse Varga", 1.7, 1, 5, 2006},
	{"W110", "Allgemeine Volkswirtschaftslehre", "2,0", "bestanden", "10.12.2025", "Prof. Tomas Reyes", 2.0, 1, 5, 2007},
	{"I180", "Wissenschaftliches Arbeiten", "bestanden", "bestanden", "01.06.2025", "Hanna Weiss", 0, 1, 3, 2008},
	{"I172", "Betriebssysteme", "2,7", "bestanden", "18.02.2026", "Dr. Paul Engel", 2.7, 1, 6, 2009},
}

func lectureDays(now time.Time) []time.Time {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var out []time.Time
	for i := 0; len(out) < 10; i++ {
		d := day.AddDate(0, 0, i)
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			out = append(out, d)
		}
	}
	return out
}

var wd = []string{"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"}

func label(t time.Time) string { return wd[t.Weekday()] + " " + t.Format("02.01.2006") }

// weekday moves a date off the weekend (demo exams are on weekdays).
func weekday(t time.Time) time.Time {
	for t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

func ev(t time.Time, start, end, kind, nr, title, room, lecturer, source string) map[string]any {
	return map[string]any{"date": label(t), "start": start, "end": end, "kind": kind, "module_nr": nr, "title": title, "room": room, "lecturer": lecturer, "source": source}
}

// agenda: lectures on weekdays, one exam, two Moodle deadlines.
func agenda(a *app.App, days int) map[string]any {
	now := a.Now().In(a.Zone)
	byDay := map[string][]map[string]any{}
	var order []string
	add := func(t time.Time, e map[string]any) {
		k := label(t)
		if _, ok := byDay[k]; !ok {
			order = append(order, k)
		}
		byDay[k] = append(byDay[k], e)
	}
	limit := now.AddDate(0, 0, days)
	for i, d := range lectureDays(now) {
		if d.After(limit) {
			break
		}
		switch i % 3 {
		case 0:
			add(d, ev(d, "09:00", "12:15", "Vorlesung", "I151", "Softwaretechnik", "A 101", "Dr. Ada Lindqvist", "cis_stundenplan"))
			add(d, ev(d, "13:00", "16:15", "Vorlesung", "I160", "Datenbanksysteme", "B 204", "Prof. Jonas Brandt", "cis_stundenplan"))
		case 1:
			add(d, ev(d, "09:00", "12:15", "Übung", "I160", "Datenbanksysteme", "PC-Pool 2", "Prof. Jonas Brandt", "cis_stundenplan"))
		case 2:
			add(d, ev(d, "10:00", "13:15", "Vorlesung", "I151", "Softwaretechnik", "A 101", "Dr. Ada Lindqvist", "cis_stundenplan"))
		}
	}
	exam := weekday(now.AddDate(0, 0, 9))
	add(exam, ev(exam, "09:00", "11:00", "Klausur", "I160", "Datenbanksysteme", "Audimax", "", "cis_pruefung"))
	d1, d2 := now.AddDate(0, 0, 1), now.AddDate(0, 0, 4)
	add(d1, ev(d1, "23:59", "23:59", "Moodle-Frist", "", "Übungsblatt 4 (I160_I24)", "", "", "moodle"))
	add(d2, ev(d2, "12:00", "12:00", "Moodle-Frist", "", "Projektabgabe Sprint 2 (I151_I24)", "", "", "moodle"))
	var out []map[string]any
	for _, k := range order {
		out = append(out, map[string]any{"date": k, "events": byDay[k]})
	}
	return map[string]any{"from": now.Format("2006-01-02"), "to": limit.Format("2006-01-02"), "days": out, "notes": []string{}}
}

func deadlines(a *app.App) []map[string]any {
	now := a.Now().In(a.Zone)
	dl := func(t time.Time, what, kind, src string) map[string]any {
		days := int(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, a.Zone).Sub(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, a.Zone)).Hours() / 24)
		return map[string]any{"when": label(t) + " " + t.Format("15:04"), "in_days": days, "what": what, "kind": kind, "source": src, "urgent": days <= 2}
	}
	at := func(d int, hh, mm int) time.Time {
		t := now.AddDate(0, 0, d)
		return time.Date(t.Year(), t.Month(), t.Day(), hh, mm, 0, 0, a.Zone)
	}
	return []map[string]any{
		dl(at(1, 23, 59), "Übungsblatt 4", "moodle_assign", "moodle"),
		dl(at(4, 12, 0), "Projektabgabe Sprint 2", "moodle_assign", "moodle"),
		dl(weekday(at(9, 9, 0)), "Klausur I160 Datenbanksysteme", "exam", "cis"),
		dl(at(12, 23, 59), "Anmeldeschluss WP Data Science Basics", "exam_registration", "cis"),
		dl(at(20, 23, 59), "Abgabe Transferleistung 6: Datenqualität im Betrieb", "transfer", "cis"),
	}
}

func courses(classification string) []map[string]any {
	type c struct {
		id                    int
		short, name, state, p string
	}
	all := []c{
		{2101, "I151_I24", "I151 - Softwaretechnik - I24a - Lindqvist", "current", "35%"},
		{2102, "I160_I24", "I160 - Datenbanksysteme - I24a/b - Brandt", "current", "52%"},
		{2110, "TuI160_I24", "Tutorium Datenbanksysteme - I24", "current", ""},
		{2111, "Transfer", "Transferleistungen Theorie / Praxis", "current", ""},
		{2103, "I168_I23", "I168 - Diskrete Mathematik 2 - I23 - Okafor", "past", "88%"},
		{2004, "I145_I23", "I145 - Diskrete Mathematik 1 - I23 - Okafor", "past", "100%"},
		{2005, "I201_I23", "I201 - Einführung in die objektorientierte Programmierung - I23 - Sorensen", "past", "100%"},
		{2006, "I154_I23", "I154 - Rechnernetze - I23 - Varga", "past", "90%"},
	}
	var out []map[string]any
	for _, x := range all {
		if classification == "current" && x.state != "current" || classification == "past" && x.state != "past" {
			continue
		}
		out = append(out, map[string]any{"id": x.id, "shortname": x.short, "name": x.name, "state": x.state, "progress": x.p, "last_access": "2026-09-30 18:12", "url": fmt.Sprintf("https://moodle.example/course/view.php?id=%d", x.id)})
	}
	return out
}

func grades() map[string]any {
	var mods []map[string]any
	passed, open, earned := 0, 0, 0
	sum, weight := 0.0, 0.0
	for _, m := range modules {
		row := map[string]any{"module_nr": m.nr, "title": m.title, "grade": m.grade, "status": m.status, "exam_date": m.examDate, "entry_date": m.examDate, "credits": fmt.Sprint(m.credits), "attempt": m.attempt}
		if m.value > 0 {
			row["grade_value"] = m.value
			sum += m.value * float64(m.credits)
			weight += float64(m.credits)
		}
		if m.status == "bestanden" {
			passed++
			earned += m.credits
		} else {
			open++
		}
		mods = append(mods, row)
	}
	return map[string]any{
		"overview": map[string]any{"student": "Max Mustermann", "modules": mods, "average": fmt.Sprintf("%.1f", sum/weight), "credits_total": fmt.Sprint(earned + 25), "seminar_credits": "4,5", "transfer_credits": "25"},
		"stats":    map[string]any{"passed": passed, "failed": 0, "open": open, "credits_earned": earned + 25, "weighted_average": sum / weight},
	}
}

func progress() map[string]any {
	g := grades()
	earned := g["stats"].(map[string]any)["credits_earned"].(int)
	var sem []map[string]any
	var mods []map[string]any
	for _, m := range modules {
		mods = append(mods, map[string]any{"module_nr": m.nr, "title": m.title, "status": m.status, "grade": m.grade, "credits": m.credits})
	}
	sem = append(sem, map[string]any{"semester": 1, "passed": 4, "open": 0, "failed": 0, "modules": mods[2:6]},
		map[string]any{"semester": 2, "passed": 3, "open": 0, "failed": 0, "modules": mods[6:]},
		map[string]any{"semester": 3, "passed": 0, "open": 2, "failed": 0, "modules": mods[:2]})
	return map[string]any{
		"thesis_requirements": map[string]any{"fulfilled": false, "modules_until_4th_missing": []string{"I160 Datenbanksysteme"}, "transferleistungen_missing": []string{"6"},
			"rule": "Alle Modulprüfungen bis einschließlich 4. Semester bestanden und Transferleistungen 1–5 erfolgreich (Bachelorthesis-Seite im CIS)."},
		"credits": map[string]any{"module_earned": earned - 25, "module_planned": 173, "transfer_earned": 25, "transfer_planned": 30, "total_earned": earned, "total_required": 210, "cis_credits_total": fmt.Sprint(earned)}, "semesters": sem}
}

func exams(a *app.App) []map[string]any {
	now := a.Now().In(a.Zone)
	d := func(n int, clock string) string {
		return weekday(now.AddDate(0, 0, n)).Format("02.01.2006") + " " + clock
	}
	return []map[string]any{
		{"exam_id": "9001", "module_nr": "I160", "title": "Datenbanksysteme", "start": d(9, "09:00"), "ende": d(9, "11:00"), "registered": true, "zenturien": []string{"I24a"}, "dozenten": []string{"Brandt, Jonas"},
			"action": "deregister", "action_label": "Abmelden", "deadlines": map[string]any{"register_closes": d(-5, "23:59"), "deregister_until": d(2, "23:59")}},
		{"exam_id": "9002", "module_nr": "I151", "title": "Softwaretechnik", "start": d(23, "09:00"), "ende": d(23, "11:00"), "registered": false, "zenturien": []string{"I24a"}, "dozenten": []string{"Lindqvist, Ada"},
			"action": "register", "action_label": "Anmelden", "deadlines": map[string]any{"register_closes": d(8, "23:59"), "deregister_until": d(16, "23:59")}},
		{"exam_id": "9003", "module_nr": "WP", "title": "WP Data Science Basics", "start": d(30, "08:00"), "registered": false, "dozenten": []string{"Rahimi, Leyla"},
			"action": "register", "action_label": "Anmelden", "deadlines": map[string]any{"register_closes": d(12, "23:59")}},
	}
}

func contents(id int) []map[string]any {
	file := func(name, size string) map[string]any {
		return map[string]any{"name": name, "size": size, "mimetype": "application/pdf", "modified": "2026-09-28 10:15", "fileurl": "https://moodle.example/pluginfile.php/" + fmt.Sprint(id) + "/" + name}
	}
	return []map[string]any{
		{"section": 0, "name": "Allgemeines", "summary": "Willkommen! Hier findet ihr Folien, Übungen und alles zur Klausur.", "modules": []map[string]any{
			{"cmid": id*10 + 1, "name": "Ankündigungen", "type": "forum", "url": "https://moodle.example/mod/forum/view.php?id=1"},
			{"cmid": id*10 + 2, "name": "Modulhandbuch", "type": "resource", "files": []map[string]any{file("modulhandbuch.pdf", "184.2 KB")}},
		}},
		{"section": 1, "name": "Woche 1 · Grundlagen", "modules": []map[string]any{
			{"cmid": id*10 + 3, "name": "Folien Kapitel 1", "type": "resource", "files": []map[string]any{file("kapitel-1.pdf", "2.4 MB")}},
			{"cmid": id*10 + 4, "name": "Übungsblatt 4", "type": "assign", "dates": map[string]any{"Fällig": "morgen 23:59"}, "text": "Bearbeitet die Aufgaben 1–3 in Zweiergruppen und ladet eine PDF hoch."},
			{"cmid": id*10 + 5, "name": "Vorlesungsaufzeichnung", "type": "url", "links": []string{"https://video.example/aufzeichnung-1"}},
		}},
		{"section": 2, "name": "Woche 2 · Vertiefung", "modules": []map[string]any{
			{"cmid": id*10 + 6, "name": "Folien Kapitel 2", "type": "resource", "files": []map[string]any{file("kapitel-2.pdf", "3.1 MB")}},
			{"cmid": id*10 + 7, "name": "Hinweis", "type": "label", "text": "Die Übung am Donnerstag findet im PC-Pool statt."},
			{"cmid": id*10 + 8, "name": "Selbsttest Kapitel 2", "type": "quiz", "url": "https://moodle.example/mod/quiz/view.php?id=8"},
		}},
	}
}

// pdf writes a tiny valid PDF so "open file" works in the demo.
func pdf(a *app.App, name, title string) (map[string]any, error) {
	dir := filepath.Join(a.DownloadDir, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	text := strings.NewReplacer("(", "", ")", "", "\\", "").Replace(title)
	body := "BT /F1 24 Tf 72 720 Td (" + text + ") Tj 0 -36 Td /F1 12 Tf (naknak Demo - erfundene Inhalte) Tj ET"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(body), body),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offs := []int{}
	for i, o := range objs {
		offs = append(offs, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	p := filepath.Join(dir, filepath.Base(name))
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	return map[string]any{"path": p, "bytes": b.Len()}, nil
}

func read(name, desc string, run func(a *app.App, args tools.Args) (any, error)) *tools.Tool {
	return &tools.Tool{Name: name, Kind: tools.Read, Desc: desc, Run: run, Params: []tools.Param{{Name: "days", Type: "integer"}, {Name: "courseid", Type: "integer"}, {Name: "classification"}, {Name: "module_nr"}, {Name: "title"}, {Name: "query"}, {Name: "discussionid", Type: "integer"}, {Name: "conversationid", Type: "integer"}, {Name: "assignid", Type: "integer"}, {Name: "limit", Type: "integer"}, {Name: "only_open", Type: "boolean"}, {Name: "mine", Type: "boolean"}, {Name: "available_only", Type: "boolean"}, {Name: "quarter"}, {Name: "seminar_id"}, {Name: "transfer_id"}}}
}

// formChanges mimics the CIS form preview: one change per given argument.
func formChanges(action string, args tools.Args, fields map[string][2]string) map[string]any {
	changes := []map[string]string{}
	for arg, f := range fields {
		v, ok := args[arg]
		if !ok {
			continue
		}
		nv := fmt.Sprint(v)
		if b, isBool := v.(bool); isBool {
			nv = map[bool]string{true: "1", false: "0"}[b]
		}
		changes = append(changes, map[string]string{"field": f[0], "old": f[1], "new": nv})
	}
	return map[string]any{"action": action, "changes": changes}
}

func write(name, desc string, preview func(args tools.Args) any) *tools.Tool {
	return &tools.Tool{Name: name, Kind: tools.Write, Desc: desc,
		Params: []tools.Param{{Name: "exam_id"}, {Name: "action"}, {Name: "seminar_id"}, {Name: "module_id"}, {Name: "termin_id"}, {Name: "choiceid", Type: "integer"}, {Name: "optionids", Type: "array:integer"}, {Name: "postid", Type: "integer"}, {Name: "message"}, {Name: "conversationid", Type: "integer"}, {Name: "text"}, {Name: "assignid", Type: "integer"}, {Name: "files", Type: "array:string"}, {Name: "online_text"}, {Name: "submit_for_grading", Type: "boolean"},
			{Name: "email"}, {Name: "infopost", Type: "boolean"}, {Name: "phone"}, {Name: "mobile"}, {Name: "fax"}, {Name: "type"}, {Name: "zusatz"}, {Name: "strasse"}, {Name: "plz"}, {Name: "ort"}, {Name: "delete", Type: "boolean"},
			{Name: "noten_betrieb", Type: "boolean"}, {Name: "anmeldungen_betrieb", Type: "boolean"}, {Name: "kommilitonen", Type: "object"}},
		Preview: func(a *app.App, args tools.Args) (any, error) { return preview(args), nil },
		Do: func(a *app.App, args tools.Args) (any, error) {
			return map[string]any{"demo": "Demo – nichts gesendet. In einer echten Instanz wäre das jetzt verbindlich passiert."}, nil
		}}
}

// Registry is the demo tool set.
func Registry() *tools.Registry {
	r := tools.NewRegistry()
	r.Add(
		read("moodle_whoami", "Demo-Nutzer", func(*app.App, tools.Args) (any, error) {
			return map[string]any{"user": "Max Mustermann", "username": "demo", "site": "Moodle (Demo)"}, nil
		}),
		read("nak_agenda", "Demo-Kalender", func(a *app.App, args tools.Args) (any, error) {
			d, _ := args.Int("days", 7)
			return agenda(a, int(d)), nil
		}),
		read("nak_deadlines", "Demo-Fristen", func(a *app.App, args tools.Args) (any, error) {
			return map[string]any{"deadlines": deadlines(a), "generated": a.Now().Format("2006-01-02 15:04")}, nil
		}),
		read("nak_dashboard", "Demo-Überblick", func(a *app.App, _ tools.Args) (any, error) {
			now := a.Now().In(a.Zone)
			return map[string]any{"now": label(now) + " " + now.Format("15:04"),
				"registered_exams": []map[string]any{{"module": "I160 Datenbanksysteme", "start": weekday(now.AddDate(0, 0, 9)).Format("02.01.2006") + " 09:00", "exam_id": "9001"}},
				"moodle_unread":    map[string]any{"notifications": 2, "conversations": 1},
				"quarter":          map[string]any{"current": map[string]any{"name": "IV/26", "from": "05.10.2026", "to": "20.12.2026"}},
				"balance":          map[string]any{"Kopierguthaben": "7,40 €"}}, nil
		}),
		read("cis_grades", "Demo-Noten", func(*app.App, tools.Args) (any, error) { return grades(), nil }),
		// the PDF already knows the Datenbanksysteme grade the page does not show yet
		read("cis_transcript_grades", "Demo-Notenübersicht", func(*app.App, tools.Args) (any, error) {
			return map[string]any{"date": "02.10.2026", "modules": []map[string]any{{"module_nr": "I160", "title": "Datenbanksysteme", "grade": "1,3", "credits": "6"}}}, nil
		}),
		read("cis_progress", "Demo-Studienfortschritt", func(*app.App, tools.Args) (any, error) { return progress(), nil }),
		read("cis_list_klausuren", "Demo-Prüfungen", func(a *app.App, _ tools.Args) (any, error) { return exams(a), nil }),
		read("moodle_courses", "Demo-Kurse", func(_ *app.App, args tools.Args) (any, error) { return courses(args.Str("classification")), nil }),
		read("moodle_course_contents", "Demo-Kursinhalt", func(_ *app.App, args tools.Args) (any, error) {
			id, _ := args.Int("courseid", 0)
			return contents(int(id)), nil
		}),
		read("moodle_course_info", "Demo-Kursinfo", func(_ *app.App, args tools.Args) (any, error) {
			return map[string]any{"category": "Bachelor Informatik", "startdate": "2026-07-27", "summary": "Ein erfundener Kurs für die naknak-Demo."}, nil
		}),
		read("nak_module", "Demo-Modul", func(a *app.App, args tools.Args) (any, error) {
			nr := strings.ToUpper(args.Str("module_nr"))
			out := map[string]any{"module_nr": nr}
			for _, m := range modules {
				if m.nr == nr {
					out["title"] = m.title
					if m.grade != "" {
						out["grade"] = map[string]any{"module_nr": m.nr, "title": m.title, "grade": m.grade, "grade_value": m.value, "status": m.status, "exam_date": m.examDate, "attempt": m.attempt}
						out["distribution"] = map[string]any{"average": 2.6, "count": 84, "lecturers": []string{m.lecturer}, "buckets": []map[string]any{{"grade": "1,0", "count": 6}, {"grade": "1,3", "count": 9}, {"grade": "1,7", "count": 12}, {"grade": "2,0", "count": 14}, {"grade": "2,3", "count": 11}, {"grade": "2,7", "count": 10}, {"grade": "3,0", "count": 8}, {"grade": "3,3", "count": 5}, {"grade": "3,7", "count": 4}, {"grade": "4,0", "count": 3}, {"grade": "5,0", "count": 2}}, "percentile": 22}
					}
					out["studienplan"] = map[string]any{"credits": m.credits, "exam_form_name": "Klausur (90 Minuten)", "exam_semester": 3}
				}
			}
			ag := agenda(a, 21)
			var next []map[string]any
			for _, d := range ag["days"].([]map[string]any) {
				for _, e := range d["events"].([]map[string]any) {
					if e["module_nr"] == nr && e["kind"] != "Klausur" {
						next = append(next, e)
					}
				}
			}
			out["next_sessions"] = next
			return out, nil
		}),
		read("moodle_whats_new", "Demo-Neuigkeiten", func(*app.App, tools.Args) (any, error) {
			return []map[string]any{
				{"course": "I160_I24", "courseid": 2102, "cmid": 21026, "activity": "Folien Kapitel 2", "type": "resource", "changes": []string{"1 neue/geänderte Datei(en)"}},
				{"course": "I151_I24", "courseid": 2101, "cmid": 21011, "activity": "Ankündigungen", "type": "forum", "changes": []string{"2 neue Beiträge"}},
				{"course": "I160_I24", "courseid": 2102, "cmid": 21024, "activity": "Übungsblatt 4", "type": "assign", "changes": []string{"Beschreibung geändert"}},
			}, nil
		}),
		read("moodle_notifications", "Demo-Benachrichtigungen", func(*app.App, tools.Args) (any, error) {
			return []map[string]any{
				{"id": 501, "subject": "Neue Bewertung: Übungsblatt 3", "time": "2026-10-01 16:40", "unread": true},
				{"id": 502, "subject": "Neuer Beitrag in Ankündigungen: Raumänderung Donnerstag", "time": "2026-10-01 09:12", "unread": true},
				{"id": 503, "subject": "Erinnerung: Projektabgabe Sprint 2", "time": "2026-09-29 08:00", "unread": false},
			}, nil
		}),
		read("moodle_conversations", "Demo-Nachrichten", func(*app.App, tools.Args) (any, error) {
			return []map[string]any{
				{"conversationid": 71, "name": "Dr. Ada Lindqvist", "members": 2, "last_message": "Gern, schicken Sie mir den Entwurf bis Freitag.", "last_time": "2026-10-01 17:05"},
				{"conversationid": 72, "name": "Lerngruppe Datenbanken", "members": 4, "last_message": "Treffen wir uns Mittwoch in der Bibliothek?", "last_time": "2026-09-30 20:41"},
			}, nil
		}),
		read("moodle_conversation_messages", "Demo-Unterhaltung", func(_ *app.App, args tools.Args) (any, error) {
			return []map[string]any{
				{"from": "Max Mustermann", "time": "2026-10-01 16:20", "text": "Guten Tag, darf ich für das Projekt eine eigene Datenbank aufsetzen?", "mine": true},
				{"from": "Dr. Ada Lindqvist", "time": "2026-10-01 17:05", "text": "Gern, schicken Sie mir den Entwurf bis Freitag."},
			}, nil
		}),
		read("moodle_assignments", "Demo-Aufgaben", func(a *app.App, args tools.Args) (any, error) {
			now := a.Now()
			all := []map[string]any{
				{"assignid": 801, "cmid": 21024, "course": "I160_I24", "courseid": 2102, "name": "Übungsblatt 4", "due": now.AddDate(0, 0, 1).Format("2006-01-02") + " 23:59", "due_unix": now.AddDate(0, 0, 1).Unix()},
				{"assignid": 802, "cmid": 21014, "course": "I151_I24", "courseid": 2101, "name": "Projektabgabe Sprint 2", "due": now.AddDate(0, 0, 4).Format("2006-01-02") + " 12:00", "due_unix": now.AddDate(0, 0, 4).Unix()},
			}
			if id, _ := args.Int("courseid", 0); id != 0 {
				var f []map[string]any
				for _, x := range all {
					if x["courseid"] == int(id) {
						f = append(f, x)
					}
				}
				return f, nil
			}
			return all, nil
		}),
		read("moodle_assignment", "Demo-Aufgabe", func(a *app.App, args tools.Args) (any, error) {
			return map[string]any{"assignid": 801, "course": "I160_I24", "name": "Übungsblatt 4", "due": a.Now().AddDate(0, 0, 1).Format("2006-01-02") + " 23:59", "max_grade": 100,
				"intro":       "Modelliert die Datenbank für den Bibliotheksverleih (ER-Diagramm + Relationenschema) und ladet eine PDF hoch.",
				"attachments": []map[string]any{{"name": "uebungsblatt-4.pdf", "size": "96.0 KB", "fileurl": "https://moodle.example/pluginfile.php/801/uebungsblatt-4.pdf"}},
				"submission":  map[string]any{"status": "new", "gradingstatus": "notgraded"}}, nil
		}),
		read("moodle_quizzes", "Demo-Tests", func(*app.App, tools.Args) (any, error) {
			return []map[string]any{{"quizid": 901, "name": "Selbsttest Kapitel 2", "course": "I160_I24", "courseid": 2102, "opens": "2026-09-28 08:00", "closes": "2026-10-12 23:59", "attempts_allowed": "unbegrenzt", "url": "https://moodle.example/mod/quiz/view.php?id=8"}}, nil
		}),
		read("moodle_choices", "Demo-Abstimmungen", func(*app.App, tools.Args) (any, error) {
			return []map[string]any{{"kind": "choice", "choiceid": 401, "cmid": 21029, "name": "Projektgruppen Sprint 3", "intro": "Tragt euch in eine Gruppe ein (max. 4 Personen).", "open": true, "closes": "2026-10-09 23:59", "multiple": false, "can_change_answer": true,
				"options": []map[string]any{{"optionid": 1, "text": "Gruppe A · Bibliothek", "taken": 3}, {"optionid": 2, "text": "Gruppe B · Mensa-App", "taken": 4, "disabled": true}, {"optionid": 3, "text": "Gruppe C · Raumbuchung", "taken": 1}}}}, nil
		}),
		write("moodle_choice_submit", "Demo: Abstimmung", func(args tools.Args) any {
			return map[string]any{"would": "Stimme abgeben", "choice": args.Str("choiceid")}
		}),
		read("moodle_forum_discussions", "Demo-Forum", func(*app.App, tools.Args) (any, error) {
			return []map[string]any{{"forum": "Ankündigungen", "forumid": 61, "discussionid": 611, "subject": "Raumänderung Donnerstag", "author": "Dr. Ada Lindqvist", "created": "2026-10-01 09:12", "last_activity": "2026-10-01 10:30", "replies": 1, "preview": "Die Übung findet diese Woche im PC-Pool 2 statt."}}, nil
		}),
		read("moodle_forum_posts", "Demo-Beiträge", func(*app.App, tools.Args) (any, error) {
			return []map[string]any{
				{"postid": 6111, "subject": "Raumänderung Donnerstag", "author": "Dr. Ada Lindqvist", "time": "2026-10-01 09:12", "message": "Die Übung findet diese Woche im PC-Pool 2 statt. Bringt eure Laptops trotzdem mit."},
				{"postid": 6112, "subject": "Re: Raumänderung Donnerstag", "author": "Lena Kaya", "time": "2026-10-01 10:30", "message": "Danke für die Info!"},
			}, nil
		}),
		read("moodle_find", "Demo-Suche", func(_ *app.App, args tools.Args) (any, error) {
			q := strings.ToLower(args.Str("query"))
			var out []map[string]any
			for _, c := range []int{2101, 2102} {
				for _, s := range contents(c) {
					for _, m := range s["modules"].([]map[string]any) {
						if strings.Contains(strings.ToLower(fmt.Sprint(m["name"])), q) {
							out = append(out, map[string]any{"courseid": c, "course": fmt.Sprintf("Kurs %d", c), "module": m["name"], "type": m["type"], "files": m["files"]})
						}
					}
				}
			}
			return out, nil
		}),
		&tools.Tool{Name: "moodle_download", Kind: tools.Local, Desc: "Demo-Datei", Params: []tools.Param{{Name: "fileurl", Required: true}},
			Run: func(a *app.App, args tools.Args) (any, error) {
				name := filepath.Base(args.Str("fileurl"))
				return pdf(a, name, strings.TrimSuffix(name, ".pdf"))
			}},
		read("eduvault_module_exams", "Demo-Altklausuren", func(_ *app.App, args tools.Args) (any, error) {
			title := args.Str("title")
			var docs []map[string]any
			for i, y := range []string{"2025", "2024", "2023", "2021"} {
				docs = append(docs, map[string]any{"id": fmt.Sprintf("00000000-0000-0000-0000-00000000000%d", i), "modul": title, "type": "exam", "jahr": y, "dozenten": "Demo", "has_solutions": i%2 == 0, "rating": 4.2 - float64(i)*0.3, "download_count": 40 - i*7})
			}
			docs = append(docs, map[string]any{"id": "00000000-0000-0000-0000-000000000009", "modul": title, "type": "probeklausur", "jahr": "2025", "has_solutions": true})
			return map[string]any{"module": title, "spellings": []string{title}, "count": len(docs), "documents": docs}, nil
		}),
		&tools.Tool{Name: "eduvault_download", Kind: tools.Local, Desc: "Demo-Altklausur", Params: []tools.Param{{Name: "exam_id", Required: true}},
			Run: func(a *app.App, args tools.Args) (any, error) {
				return pdf(a, "altklausur-"+args.Str("exam_id")[30:]+".pdf", "Altklausur (Demo)")
			}},
		read("cis_list_seminars", "Demo-Seminare", func(_ *app.App, args tools.Args) (any, error) {
			mine := []map[string]any{
				{"id": "7001", "title": "Moderation und Präsentation", "lecturer": "Clara Yilmaz", "from": "Samstag, 07.03.2026", "to": "Sonntag, 08.03.2026", "info": "Sa. und So. 9:00-16:30 Uhr", "category": "Methodenkompetenz / Kommunikation", "status": []string{"Seminar besucht", "Teilnehmer am Seminar"}},
			}
			if args.Bool("mine", false) {
				return map[string]any{"quarter": "Meine Seminare", "seminars": mine}, nil
			}
			return map[string]any{"quarter": "2026 - 4", "available_quarters": map[string]any{"2026 - 3": "903", "2026 - 4": "904"}, "notice": "Anmeldungen sind bis 14 Tage vor Beginn möglich.",
				"seminars": []map[string]any{
					{"id": "7101", "title": "Agiles Projektmanagement in der Praxis", "lecturer": "Jonas Weber", "from": "Samstag, 07.11.2026", "to": "Sonntag, 08.11.2026", "info": "Sa. und So. 9:00-16:30 Uhr", "category": "Methodenkompetenz / Management", "actions": []map[string]any{{"action": "subscribeToSeminar", "label": "zum Seminar anmelden"}}},
					{"id": "7102", "title": "Ethik der künstlichen Intelligenz", "lecturer": "Dr. Nadia Petrov", "from": "Freitag, 20.11.2026", "to": "Samstag, 21.11.2026", "info": "Fr. 14:00-18:00, Sa. 9:00-16:30", "category": "Ethik / Soziales", "has_waitlist": true, "actions": []map[string]any{{"action": "anWartelisteAnmelden", "label": "auf die Warteliste"}}},
				}}, nil
		}),
		read("cis_seminar_detail", "Demo-Seminardetail", func(*app.App, tools.Args) (any, error) {
			return map[string]any{"themenbereich": "Methodenkompetenz", "pruefungsform": "Teilnahme", "kreditpunkte": "1,5", "workload": "16 Stunden", "bemerkung": "Bitte Laptop mitbringen."}, nil
		}),
		read("cis_seminar_participation", "Demo-Belegung", func(*app.App, tools.Args) (any, error) {
			return map[string]any{"teilnehmende": 14, "warteliste": 0, "mindestteilnehmerzahl": 16, "du": "nicht angemeldet"}, nil
		}),
		read("cis_list_wahlpflicht", "Demo-Wahlpflicht", func(*app.App, tools.Args) (any, error) {
			return map[string]any{"chosen": []map[string]any{{"id": "8801", "name": "WP Data Engineering", "termin": "Q3/26", "chosen": true}}, "available": nil, "selection_open": false,
				"notice": "Der nächste Wahlzeitraum beginnt im November."}, nil
		}),
		read("cis_list_transfer", "Demo-Transferleistungen", func(*app.App, tools.Args) (any, error) { return demoTransfers(), nil }),
		read("nak_transfer_plan", "Demo-Zeitplan der Transferleistungen", func(a *app.App, _ tools.Args) (any, error) {
			var list []transfer.Report
			for _, x := range demoTransfers() {
				list = append(list, transfer.Report{No: fmt.Sprint(x["no"]), Abgabedatum: fmt.Sprint(x["abgabedatum"]), Wertung: fmt.Sprint(x["wertung"])})
			}
			plan := transfer.BuildPlan("Angewandte Informatik (B.Sc.)", "I24a", nil, list, a.Now().In(a.Zone))
			if aid, ok := demoThesis(a)["planning_aid"].(thesis.PlanningAid); ok {
				if to, err := time.ParseInLocation("02.01.2006", aid.LatestRegisterTo, a.Zone); err == nil {
					plan.ThesisRegisterUntil, plan.ThesisRegisterBy = aid.LatestRegisterTo, to.AddDate(0, 0, -7*thesis.TLWeeks).Format("02.01.2006")
				}
			}
			return plan, nil
		}),
		read("cis_transfer_bewertung", "Demo-Bewertung", func(*app.App, tools.Args) (any, error) {
			return map[string]any{"kriterien": []map[string]any{{"kriterium": "Problemstellung", "note": "1,7", "gewichtung": "20 %"}, {"kriterium": "Methodik", "note": "2,0", "gewichtung": "40 %"}, {"kriterium": "Form", "note": "1,3", "gewichtung": "40 %"}}, "gesamt": "1,7"}, nil
		}),
		read("cis_list_certs", "Demo-Bescheinigungen", func(*app.App, tools.Args) (any, error) {
			return []map[string]any{
				{"name": "Studienbescheinigung WS 2026 (de)", "semester": "WS 2026", "period": "01.10.2026 - 31.03.2027", "lang": "de", "download_url": "https://cis.example/cert/ws2026-de"},
				{"name": "Certificate of enrolment WS 2026 (en)", "semester": "WS 2026", "period": "01.10.2026 - 31.03.2027", "lang": "en", "download_url": "https://cis.example/cert/ws2026-en"},
			}, nil
		}),
		read("cis_profile", "Demo-Profil", func(*app.App, tools.Args) (any, error) {
			return map[string]any{"all": map[string]any{"Vorname": "Max", "Nachname": "Mustermann", "Matrikelnr.": "99999", "Zenturie": "I24a", "Studiengang": "Angewandte Informatik", "Firma": "Beispiel GmbH", "Email (NAK)": "max.mustermann@nordakademie.example"}}, nil
		}),
		read("cis_sharing", "Demo-Freigaben", func(*app.App, tools.Args) (any, error) {
			return map[string]any{
				"betrieb":      map[string]any{"noten_fuer_betrieb_freigegeben": true, "pruefungsanmeldungen_fuer_betrieb_freigegeben": false},
				"kommilitonen": map[string]any{"Name": "2 (alle Zenturien)", "Firma": "1 (nur meine Zenturie)", "Geburtsdatum": "0 (nicht anzeigen)", "Adresse": "0 (nicht anzeigen)", "Fest": "0 (nicht anzeigen)", "Mobil": "1 (nur meine Zenturie)", "Mail": "2 (alle Zenturien)"},
				"levels":       map[string]string{"0": "nicht anzeigen", "1": "nur meine Zenturie", "2": "alle Zenturien"},
			}, nil
		}),
		read("cis_contact", "Demo-Kontakt", func(*app.App, tools.Args) (any, error) {
			return map[string]any{"email_privat": "max@example.org", "infopost": true, "telefon_festnetz": "", "telefon_mobil": "0170-0000000", "fax": ""}, nil
		}),
		read("cis_address", "Demo-Adresse", func(_ *app.App, args tools.Args) (any, error) {
			types := map[string]string{"1": "Hauptadresse", "5": "Semesteradresse"}
			if args.Str("type") == "5" {
				return map[string]any{"type_id": "5", "type_name": "Semesteradresse", "available_types": types, "strasse": "Köllner Chaussee 11", "plz": "25337", "ort": "Elmshorn", "letter_form": "Max Mustermann, Köllner Chaussee 11, 25337 Elmshorn"}, nil
			}
			return map[string]any{"type_id": "1", "type_name": "Hauptadresse", "available_types": types, "strasse": "Musterweg 1", "plz": "20095", "ort": "Hamburg", "letter_form": "Max Mustermann, Musterweg 1, 20095 Hamburg"}, nil
		}),
		write("cis_update_contact", "Demo: Kontakt", func(args tools.Args) any {
			return []any{formChanges("Kontaktdaten ändern", args, map[string][2]string{
				"email": {"email.privat", "max@example.org"}, "infopost": {"email.infopost", "1"}, "phone": {"telefon.fest", ""}, "mobile": {"telefon.mobil", "0170-0000000"}, "fax": {"telefon.fax", ""}})}
		}),
		write("cis_update_address", "Demo: Adresse", func(args tools.Args) any {
			if args.Bool("delete", false) {
				return map[string]any{"action": "Adresse löschen", "changes": []map[string]string{{"field": "adresse", "old": "Köllner Chaussee 11, 25337 Elmshorn", "new": ""}}}
			}
			return formChanges("Adresse ändern", args, map[string][2]string{
				"strasse": {"strasse", "Musterweg 1"}, "zusatz": {"zusatz", ""}, "plz": {"plz", "20095"}, "ort": {"ort", "Hamburg"}})
		}),
		write("cis_set_sharing", "Demo: Freigaben", func(args tools.Args) any {
			out := formChanges("Freigaben ändern", args, map[string][2]string{"noten_betrieb": {"notenfreigabe", "1"}, "anmeldungen_betrieb": {"anmeldungfreigabe", "0"}})
			if k, ok := args["kommilitonen"].(map[string]any); ok {
				for f, v := range k {
					out["changes"] = append(out["changes"].([]map[string]string), map[string]string{"field": f, "old": "?", "new": fmt.Sprint(v)})
				}
			}
			return out
		}),
		read("nak_transfer_grades", "Demo-Transfernoten", func(*app.App, tools.Args) (any, error) {
			item := func(id, no, topic, module, nr string, v float64) map[string]any {
				return map[string]any{"id": id, "no": no, "topic": topic, "module": module, "module_nr": nr, "wertung": "bestanden", "versuch": "1",
					"exact": v, "grade": transfer.CriteriaValue(v), "final": transfer.FinalGrade(v), "criteria": 12, "must_pass_ok": true}
			}
			return map[string]any{"items": []map[string]any{
				item("6597", "1", "Kennzahlen für den Service Desk", "Allgemeine Betriebswirtschaftslehre (I169)", "I169", 2.2),
				item("6598", "2", "Auswahl einer CI/CD-Plattform", "Praxis der Softwareentwicklung (I143)", "I143", 1.75),
			}, "average": "1,98", "final": "1,9", "count": 2, "note": transfer.GradesNote}, nil
		}),
		read("nak_thesis", "Demo-Bachelorthesis", func(a *app.App, _ tools.Args) (any, error) { return demoThesis(a), nil }),
		&tools.Tool{Name: "nak_thesis_calc", Kind: tools.Read, Desc: "Demo-Rechner", Params: []tools.Param{{Name: "start"}, {Name: "registration"}},
			Run: func(a *app.App, args tools.Args) (any, error) {
				qs, ds := demoThesisDates(a)
				if s := args.Str("start"); s != "" {
					t, err := time.ParseInLocation("2006-01-02", s, a.Zone)
					if err != nil {
						return nil, err
					}
					return thesis.Calc(t, qs, ds), nil
				}
				t, err := time.ParseInLocation("2006-01-02", args.Str("registration"), a.Zone)
				if err != nil {
					return nil, fmt.Errorf("start oder registration angeben (YYYY-MM-DD)")
				}
				return thesis.Calc(thesis.StartFor(t), qs, ds), nil
			}},
		&tools.Tool{Name: "nak_thesis_match", Kind: tools.Read, Desc: "Demo-Betreuungs-Assistent", Params: []tools.Param{{Name: "topic"}, {Name: "reviewer"}},
			Run: func(a *app.App, args tools.Args) (any, error) {
				return tools.MatchResult(args.Str("topic"), args.Str("reviewer"), demoReviewers())
			}},
		&tools.Tool{Name: "nak_thesis_reviewers", Kind: tools.Read, Desc: "Demo-Gutachtende", Params: []tools.Param{{Name: "query"}},
			Run: func(a *app.App, args tools.Args) (any, error) {
				return thesis.FilterReviewers(demoReviewers(), args.Str("query")), nil
			}},
		read("cis_status", "Demo-Status", func(*app.App, tools.Args) (any, error) {
			return map[string]any{"name": "Max Mustermann", "zenturie": "I24a", "studiengang": "Angewandte Informatik (B.Sc.)"}, nil
		}),
		read("cis_timetable", "Demo-Stundenplan", func(a *app.App, args tools.Args) (any, error) { return timetable(a, args), nil }),
		read("mensa_menu", "Demo-Speiseplan", func(a *app.App, _ tools.Args) (any, error) { return demoMenu(a.Now().In(a.Zone)), nil }),
		read("mensa_account", "Demo-Mensakarte", func(a *app.App, _ tools.Args) (any, error) {
			return mensa.Account{Balance: 12.35, Card: "10001", Bookings: demoBookings(a.Now().In(a.Zone))}, nil
		}),
		read("mensa_spending", "Demo-Mensaausgaben", func(a *app.App, _ tools.Args) (any, error) {
			return map[string]any{"balance": 12.35, "spending": mensa.Spend(demoBookings(a.Now().In(a.Zone)))}, nil
		}),
		read("cis_balance", "Demo-Guthaben", func(*app.App, tools.Args) (any, error) { return map[string]any{"Kopierguthaben": "7,40 €"}, nil }),
		&tools.Tool{Name: "cis_download_cert", Kind: tools.Local, Desc: "Demo-Bescheinigung", Params: []tools.Param{{Name: "download_url"}},
			Run: func(a *app.App, args tools.Args) (any, error) {
				return pdf(a, "studienbescheinigung.pdf", "Studienbescheinigung (Demo)")
			}},
		&tools.Tool{Name: "cis_transcript", Kind: tools.Local, Desc: "Demo-Notenspiegel", Params: []tools.Param{{Name: "lang"}},
			Run: func(a *app.App, args tools.Args) (any, error) {
				return pdf(a, "notenspiegel-"+args.Str("lang")+".pdf", "Notenspiegel (Demo)")
			}},
		write("cis_seminar_action", "Demo: Seminaranmeldung", func(args tools.Args) any {
			return map[string]any{"would": args.Str("action"), "seminar": args.Str("seminar_id")}
		}),
		write("cis_select_wahlpflicht", "Demo: Wahlpflicht", func(args tools.Args) any {
			return map[string]any{"would": "Wahlpflichtmodul wählen", "module": args.Str("module_id")}
		}),
		write("cis_klausur_action", "Demo: Prüfungsanmeldung", func(args tools.Args) any {
			return map[string]any{"action": map[string]string{"register": "Prüfungsanmeldung", "deregister": "Prüfungsabmeldung"}[args.Str("action")], "exam": "Demo-Prüfung " + args.Str("exam_id")}
		}),
		write("moodle_forum_reply", "Demo: Forenantwort", func(args tools.Args) any {
			return map[string]any{"would": "Antwort posten", "message": args.Str("message")}
		}),
		write("moodle_send_message", "Demo: Nachricht", func(args tools.Args) any {
			return map[string]any{"would": "Nachricht senden", "text": args.Str("text")}
		}),
		write("moodle_assignment_submit", "Demo: Abgabe", func(args tools.Args) any {
			return map[string]any{"would": "Abgabe hochladen", "files": args.Strs("files"), "submit_for_grading": args.Bool("submit_for_grading", false)}
		}),
		write("moodle_mark_notifications_read", "Demo: gelesen", func(tools.Args) any { return map[string]any{"would": "Alle Benachrichtigungen als gelesen markieren"} }),
		write("moodle_mark_messages_read", "Demo: gelesen", func(tools.Args) any { return map[string]any{"would": "Alle Moodle-Nachrichten als gelesen markieren"} }),
	)
	return r
}

// timetable: lectures Monday to Thursday, with an invented Praxisphase
// (weeks 1 to 3 from now) so the grade deadline has something to skip.
func timetable(a *app.App, args tools.Args) map[string]any {
	now := a.Now().In(a.Zone)
	from, err := time.ParseInLocation("2006-01-02", args.Str("from"), a.Zone)
	if err != nil {
		from = now
	}
	to, err := time.ParseInLocation("2006-01-02", args.Str("to"), a.Zone)
	if err != nil {
		to = from.AddDate(0, 0, 7)
	}
	var evs []map[string]any
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		weeks := int(d.Sub(now).Hours() / 24 / 7)
		if d.Weekday() == time.Friday || d.Weekday() == time.Saturday || d.Weekday() == time.Sunday || (weeks >= 1 && weeks <= 3) {
			continue
		}
		at := time.Date(d.Year(), d.Month(), d.Day(), 9, 0, 0, 0, a.Zone)
		evs = append(evs, map[string]any{"start": at, "end": at.Add(3 * time.Hour), "kind": "V", "kind_name": "Vorlesung", "module_nr": "I151", "title": "Softwaretechnik", "room": "A 101"})
	}
	return map[string]any{"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02"), "events": evs, "count": len(evs)}
}

// WrittenExam is a registered exam from six days ago that has no grade yet,
// so the demo shows a pending grade with its deadline.
func WrittenExam(now time.Time) []map[string]any {
	return []map[string]any{{"exam_id": "8800", "module_nr": "I190", "title": "Theoretische Informatik", "start": now.AddDate(0, 0, -6).Format("02.01.2006") + " 09:00", "registered": true, "dozenten": []string{"Okafor, Mira"}}}
}

// demoTransfers fit the AINF plan of cohort 2024: T1 in its phase, T2 late,
// T3 under way in the current one.
func demoTransfers() []map[string]any {
	return []map[string]any{
		{"id": "6597", "no": "1", "abgabedatum": "30.09.2025", "topic": "Kennzahlen für den Service Desk", "module": "Allgemeine Betriebswirtschaftslehre (I169)", "wertung": "bestanden", "versuch": "1", "status": "bewertet"},
		{"id": "6598", "no": "2", "abgabedatum": "10.05.2026", "topic": "Auswahl einer CI/CD-Plattform", "module": "Praxis der Softwareentwicklung (I143)", "wertung": "bestanden", "versuch": "1", "status": "bewertet"},
		{"id": "6602", "no": "3", "abgabedatum": "09.10.2026", "korrekturfrist": "06.11.2026", "topic": "Datenqualität im Betrieb", "module": "Datenbanksysteme (I160)", "wertung": "", "versuch": "1", "status": "angemeldet"},
		{"id": "6611", "no": "4", "abgabedatum": time.Now().AddDate(0, 0, 10).Format("02.01.2006"), "topic": "Standardisierte Projektsteckbriefe", "module": "IT-Organisation und Projektmanagement (I162)", "wertung": "", "versuch": "1",
			"status": "Erstellungszeit läuft", "action": "upload", "running": true, "action_url": "https://cis.nordakademie.de/studium/bachelor/transferleistungen-praxisberichte"},
	}
}

// demoThesisDates: typical NORDAKADEMIE quarters and graduation deadlines
// around now, so the thesis tools compute real dates for the demo student.
func demoThesisDates(a *app.App) ([]thesis.Quarter, []thesis.Deadline) {
	var qs []thesis.Quarter
	var ds []thesis.Deadline
	y0 := a.Now().In(a.Zone).Year() - 1
	at := func(y, m, d int) time.Time { return time.Date(y, time.Month(m), d, 0, 0, 0, 0, a.Zone) }
	for y := y0; y <= y0+4; y++ {
		yy := fmt.Sprintf("%02d", y%100)
		qs = append(qs, thesis.Quarter{Name: "I/" + yy, From: at(y, 1, 10), To: at(y, 3, 18)}, thesis.Quarter{Name: "II/" + yy, From: at(y, 4, 15), To: at(y, 6, 20)},
			thesis.Quarter{Name: "III/" + yy, From: at(y, 7, 28), To: at(y, 10, 3)}, thesis.Quarter{Name: "IV/" + yy, From: at(y, 10, 13), To: at(y, 12, 19)})
		ds = append(ds, thesis.Deadline{Name: "März " + fmt.Sprint(y), LastGrades: at(y, 3, 24), Board: at(y, 3, 31), Ceremony: fmt.Sprintf("Bachelor-Graduierung: 16.04.%d", y)},
			thesis.Deadline{Name: "Juni " + fmt.Sprint(y), LastGrades: at(y, 6, 1), Board: at(y, 6, 8)},
			thesis.Deadline{Name: "September " + fmt.Sprint(y), LastGrades: at(y, 9, 23), Board: at(y, 9, 30)},
			thesis.Deadline{Name: "November " + fmt.Sprint(y), LastGrades: at(y, 11, 17), Board: at(y, 11, 24)})
	}
	return qs, ds
}

// demoThesis: cohort 2024 (Zenturie I24a) graduates in March three and a half
// years later; the planning aid is derived the way the CIS publishes it.
func demoThesis(a *app.App) map[string]any {
	qs, ds := demoThesisDates(a)
	gy := 2028
	var target thesis.Deadline
	for _, d := range ds {
		if d.Name == fmt.Sprintf("März %d", gy) {
			target = d
		}
	}
	l := thesis.Latest(target, qs, ds)
	earliestReg := time.Date(gy-1, 10, 9, 0, 0, 0, 0, a.Zone)
	aid := thesis.PlanningAid{Found: true, Cohort: "2024", EarliestRegistration: earliestReg.Format("02.01.2006"),
		EarliestStart: thesis.StartFor(earliestReg).Format("02.01.2006"), Semester7From: fmt.Sprintf("25.10.%d", gy-1), Semester7To: fmt.Sprintf("12.11.%d", gy-1),
		LatestRegisterKW: fmt.Sprint(l.KW), LatestRegisterFrom: l.RegisterWeekFrom, LatestRegisterTo: l.RegisterWeekTo, LatestStart: l.Start, LatestSubmission: l.Submission,
		RepeatFrom: fmt.Sprintf("24.01.%d", gy), RepeatTo: fmt.Sprintf("04.02.%d", gy), LastGrades: target.LastGrades.Format("02.01.2006"), Board: target.Board.Format("02.01.2006"), Ceremony: fmt.Sprintf("16.04.%d", gy)}
	now := a.Now().In(a.Zone)
	latest := []map[string]any{}
	for _, d := range ds {
		x := thesis.Latest(d, qs, ds)
		to, _ := time.ParseInLocation("02.01.2006", x.RegisterWeekTo, a.Zone)
		if !x.Found || to.Before(now) || len(latest) >= 5 {
			continue
		}
		latest = append(latest, map[string]any{"latest": x, "reachable": !to.Before(earliestReg)})
	}
	return map[string]any{
		"eligibility":  thesis.Eligibility{Known: true, Reasons: []string{"Es wurden nicht alle benötigten Transferleistungen bestanden."}},
		"planning_aid": aid, "latest": latest,
		"transfer": map[string]any{"t1_5_passed": 2, "t6": "offen"},
		"rules":    map[string]any{"months": thesis.Months, "extension_weeks": thesis.ExtensionWeeks, "review_lecture_weeks": thesis.ReviewWeeks, "start_after_weeks": thesis.StartAfterWeeks, "tl_weeks": thesis.TLWeeks},
	}
}

func demoReviewers() []thesis.Reviewer {
	r := func(name, dept string, load int, areas ...string) thesis.Reviewer {
		return thesis.Reviewer{Name: name, Email: strings.ToLower(strings.ReplaceAll(strings.Split(name, ", ")[1]+"."+strings.Split(name, ", ")[0], " ", "")) + "@nordakademie.example",
			Department: dept, Areas: areas, Load: load, LoadLabel: map[int]string{0: "frei", 1: "mittel", 2: "voll"}[load]}
	}
	return []thesis.Reviewer{
		r("Brandt, Jonas", "Informatik", 0, "Datenbanksysteme", "Data Engineering", "Softwarearchitektur"),
		r("Lindqvist, Ada", "Informatik", 1, "Softwaretechnik", "Agile Methoden", "Testautomatisierung"),
		r("Okafor, Mira", "Informatik", 0, "Diskrete Mathematik", "Kryptographie", "IT-Sicherheit"),
		r("Varga, Ilse", "Informatik", 2, "Rechnernetze", "Cloud Computing", "DevOps"),
		r("Weiss, Hanna", "Wirtschaftswissenschaften", 0, "Wissenschaftliches Arbeiten", "Projektmanagement", "Change Management"),
		r("Engel, Paul", "Informatik, Wirtschaftswissenschaften", 0, "Betriebliche Anwendungssysteme", "ERP-Systeme", "Prozessmanagement"),
	}
}

var demoDishes = [][2]string{
	{"Hähnchen-Curry mit Basmatireis", "Gemüse-Curry mit Basmatireis"},
	{"Spaghetti Bolognese", "Spaghetti mit Linsenbolognese"},
	{"Kartoffelsuppe mit Würstchen", "Kartoffelsuppe mit Kräutercroûtons"},
	{"Schnitzel mit Pommes und Salat", "Gemüseschnitzel mit Pommes und Salat"},
	{"Fischfilet mit Kartoffeln und Remoulade", "Falafel mit Couscous und Joghurtdip"},
}

// two weeks of menus from this Monday on
func demoMenu(now time.Time) map[string]any {
	names := []string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"}
	monday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -((int(now.Weekday()) + 6) % 7))
	var days []map[string]any
	for i := 0; i < 12; i++ {
		d := monday.AddDate(0, 0, i)
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		pair := demoDishes[(i+d.YearDay())%len(demoDishes)]
		date := d.Format("2006-01-02")
		days = append(days, map[string]any{"date": date, "weekday": names[d.Weekday()], "dishes": []mensa.Dish{
			{Date: date, Line: "Menü 1", Name: pair[0], Price: 5, Tags: []string{"Geflügel"}, Allergens: []string{"Glutenhaltiges Getreide", "Milch"}},
			{Date: date, Line: "Menü 2 Veg.", Name: pair[1], Price: 5, Tags: []string{"Vegetarisch"}, Allergens: []string{"Sellerie"}},
		}})
	}
	return map[string]any{"days": days}
}

func demoBookings(now time.Time) []mensa.Booking {
	var out []mensa.Booking
	for i := 1; i <= 160; i++ {
		d := now.AddDate(0, 0, -i*3)
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		at := time.Date(d.Year(), d.Month(), d.Day(), 12, 40, 0, 0, now.Location())
		out = append(out, mensa.Booking{At: at, Item: "Menü 1", Dish: demoDishes[i%len(demoDishes)][i%2], Qty: 1, Amount: -5})
		if i%4 == 0 {
			out = append(out, mensa.Booking{At: at.Add(-time.Minute), Item: "Becher Filterkaffee 0,3", Qty: 1, Amount: -1})
		}
		if i%10 == 0 {
			out = append(out, mensa.Booking{At: at.Add(-2 * time.Minute), Item: "Aufladung 50€", Qty: 1, Amount: 50})
		}
	}
	return out
}
