package tools

import (
	"github.com/Raindancer118/nak-api/internal/app"
	"github.com/Raindancer118/nak-api/internal/moodle"
)

func ms(a *app.App) (*moodle.Service, error) { return a.Moodle() }

// mread wraps a read-only Moodle handler.
func mread(name, desc string, params []Param, f func(s *moodle.Service, a *app.App, args Args) (any, error)) *Tool {
	return &Tool{Name: name, Desc: desc, Kind: Read, Params: params, Run: func(a *app.App, args Args) (any, error) {
		s, err := ms(a)
		if err != nil {
			return nil, err
		}
		return f(s, a, args)
	}}
}

// mwrite builds a gated tool from a Moodle use case taking a confirm flag.
func mwrite(name, desc string, params []Param, f func(s *moodle.Service, a *app.App, args Args, confirm bool) (any, error)) *Tool {
	call := func(confirm bool) Handler {
		return func(a *app.App, args Args) (any, error) {
			s, err := ms(a)
			if err != nil {
				return nil, err
			}
			return f(s, a, args, confirm)
		}
	}
	return &Tool{Name: name, Desc: desc, Kind: Write, Params: params, Preview: call(false), Do: call(true)}
}

func moodleTools() []*Tool {
	return []*Tool{
		mread("moodle_whoami", "Eingeloggter Moodle-Nutzer, Seite und Moodle-Version.", nil,
			func(s *moodle.Service, a *app.App, args Args) (any, error) { return s.Whoami() }),
		mread("moodle_courses", "Eingeschriebene Kurse (id, Kurzname, Name, Status, Fortschritt). Die id wird von fast allen anderen Tools als courseid gebraucht.",
			[]Param{{Name: "query", Desc: "Filter auf Kurs-/Kurzname (Teilwörter, case-insensitive)"},
				{Name: "classification", Desc: "Zeitlicher Filter, Standard all", Enum: []string{"all", "current", "past", "future"}}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				return s.Courses(args.Str("query"), args.Str("classification"))
			}),
		mread("moodle_course_contents", "Inhalt eines Kurses: Abschnitte mit Aktivitäten, Terminen, Texten und Dateien (inkl. fileurl).",
			[]Param{{Name: "courseid", Type: "integer", Required: true, Desc: "Kurs-id aus moodle_courses"}, {Name: "section", Type: "integer", Desc: "Nur diesen Abschnitt (Nummer) zeigen"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				id, err := args.ReqInt("courseid")
				if err != nil {
					return nil, err
				}
				sec, err := args.IntPtr("section")
				if err != nil {
					return nil, err
				}
				return s.CourseContents(id, sec)
			}),
		mread("moodle_find", "Sucht Aktivitäten und Dateien per Name über alle (oder ausgewählte) Kurse, z.B. 'Foliensatz 9' oder 'klausur pdf'.",
			[]Param{{Name: "query", Required: true, Desc: "Suchbegriffe (alle müssen vorkommen)"}, {Name: "courseids", Type: "array:integer", Desc: "Auf diese Kurs-ids beschränken"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				ids, err := args.Ints("courseids")
				if err != nil {
					return nil, err
				}
				return s.Find(args.Str("query"), ids)
			}),
		mread("moodle_upcoming", "Anstehende Fristen/Termine aus dem Moodle-Kalender (Abgaben, Tests, Abstimmungen).",
			[]Param{{Name: "days", Type: "integer", Desc: "Zeitraum in Tagen ab jetzt, Standard 14"}, {Name: "courseid", Type: "integer", Desc: "Nur dieser Kurs"},
				{Name: "limit", Type: "integer", Desc: "Max. Einträge (≤50), Standard 50"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				days, err := args.Int("days", 14)
				if err != nil {
					return nil, err
				}
				cid, err := args.IntPtr("courseid")
				if err != nil {
					return nil, err
				}
				limit, err := args.Int("limit", 50)
				if err != nil {
					return nil, err
				}
				return s.Upcoming(days, cid, limit)
			}),
		mread("moodle_assignments", "Aufgaben (Abgaben) mit Fristen, nach Fälligkeit sortiert.",
			[]Param{{Name: "courseid", Type: "integer", Desc: "Nur dieser Kurs"}, {Name: "only_open", Type: "boolean", Desc: "Nur noch offene (Standard true)"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				cid, err := args.IntPtr("courseid")
				if err != nil {
					return nil, err
				}
				return s.Assignments(cid, args.Bool("only_open", true))
			}),
		mread("moodle_assignment", "Details einer Aufgabe: Aufgabentext, Anhänge, eigener Abgabestatus, Bewertung und Feedback.",
			[]Param{{Name: "assignid", Type: "integer", Required: true, Desc: "assignid aus moodle_assignments"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				id, err := args.ReqInt("assignid")
				if err != nil {
					return nil, err
				}
				return s.Assignment(id)
			}),
		mread("moodle_grades", "Noten. Ohne courseid: Gesamtnote je Kurs. Mit courseid: alle Bewertungsaspekte inkl. Feedback.",
			[]Param{{Name: "courseid", Type: "integer", Desc: "Kurs-id für Detailansicht"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				cid, err := args.IntPtr("courseid")
				if err != nil {
					return nil, err
				}
				return s.Grades(cid)
			}),
		mread("moodle_forum_discussions", "Forendiskussionen eines Kurses (alle Foren) oder eines Forums, neueste zuerst.",
			[]Param{{Name: "courseid", Type: "integer", Desc: "Kurs-id"}, {Name: "forumid", Type: "integer", Desc: "Forum-id"},
				{Name: "limit", Type: "integer", Desc: "Diskussionen pro Forum, Standard 10"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				cid, err := args.IntPtr("courseid")
				if err != nil {
					return nil, err
				}
				fid, err := args.IntPtr("forumid")
				if err != nil {
					return nil, err
				}
				limit, err := args.Int("limit", 10)
				if err != nil {
					return nil, err
				}
				return s.ForumDiscussions(cid, fid, limit)
			}),
		mread("moodle_forum_posts", "Alle Beiträge einer Forendiskussion im Volltext, chronologisch.",
			[]Param{{Name: "discussionid", Type: "integer", Required: true, Desc: "discussionid aus moodle_forum_discussions"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				id, err := args.ReqInt("discussionid")
				if err != nil {
					return nil, err
				}
				return s.ForumPosts(id)
			}),
		mread("moodle_notifications", "Moodle-Benachrichtigungen (neue Bewertungen, Forenbeiträge, Fristen …).",
			[]Param{{Name: "limit", Type: "integer", Desc: "Standard 20"}, {Name: "unread_only", Type: "boolean", Desc: "Nur ungelesene"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				limit, err := args.Int("limit", 20)
				if err != nil {
					return nil, err
				}
				return s.Notifications(limit, args.Bool("unread_only", false))
			}),
		mread("moodle_conversations", "Moodle-Nachrichten: Unterhaltungen mit letzter Nachricht.",
			[]Param{{Name: "limit", Type: "integer", Desc: "Standard 20"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				limit, err := args.Int("limit", 20)
				if err != nil {
					return nil, err
				}
				return s.Conversations(limit)
			}),
		mread("moodle_conversation_messages", "Nachrichten einer Unterhaltung, chronologisch.",
			[]Param{{Name: "conversationid", Type: "integer", Required: true, Desc: "aus moodle_conversations"}, {Name: "limit", Type: "integer", Desc: "Neueste n Nachrichten, Standard 50"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				id, err := args.ReqInt("conversationid")
				if err != nil {
					return nil, err
				}
				limit, err := args.Int("limit", 50)
				if err != nil {
					return nil, err
				}
				return s.ConversationMessages(id, limit)
			}),
		mread("moodle_read", "Liest eine Moodle-Datei (fileurl) als Text: PDFs (seitenweise), HTML-Seiten, Textdateien. Andere Formate → moodle_download.",
			[]Param{{Name: "fileurl", Required: true, Desc: "fileurl aus moodle_course_contents/moodle_find"}, {Name: "max_chars", Type: "integer", Desc: "Standard 40000"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				n, err := args.Int("max_chars", 40000)
				if err != nil {
					return nil, err
				}
				t, err := s.Read(args.Str("fileurl"), int(n))
				if err != nil {
					return nil, err
				}
				return map[string]any{"text": t}, nil
			}),
		{Name: "moodle_download", Kind: Local, Desc: "Lädt eine Moodle-Datei (fileurl) herunter und gibt den lokalen Pfad zurück. Überschreibt nie, bei Namensgleichheit wird ' (n)' angehängt.",
			Params: []Param{{Name: "fileurl", Required: true, Desc: "fileurl aus moodle_course_contents/moodle_find"}, {Name: "target_dir", Desc: "Zielordner, Standard ~/Downloads/moodle"}},
			Run: func(a *app.App, args Args) (any, error) {
				s, err := ms(a)
				if err != nil {
					return nil, err
				}
				dir := args.Str("target_dir")
				if dir == "" {
					dir = "~/Downloads/moodle"
				}
				return s.Download(args.Str("fileurl"), a.ExpandPath(dir))
			}},
		mread("moodle_dashboard", "Schnellüberblick: ungelesene Benachrichtigungen/Nachrichten und Fristen der nächsten 7 Tage. Guter Einstieg.", nil,
			func(s *moodle.Service, a *app.App, args Args) (any, error) { return s.Dashboard() }),
		mread("moodle_whats_new", "Was hat sich in Kursen geändert (neue Dateien, Forenbeiträge, geänderte Aktivitäten)? Ohne courseids: alle laufenden Kurse.",
			[]Param{{Name: "courseids", Type: "array:integer", Desc: "Nur diese Kurse"}, {Name: "days", Type: "integer", Desc: "Zeitraum in Tagen, Standard 7"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				ids, err := args.Ints("courseids")
				if err != nil {
					return nil, err
				}
				days, err := args.Int("days", 7)
				if err != nil {
					return nil, err
				}
				return s.WhatsNew(ids, days)
			}),
		mread("moodle_course_info", "Kursdetails: Dozenten, Kategorie, Laufzeit, Kursbeschreibung.",
			[]Param{{Name: "courseid", Type: "integer", Required: true, Desc: "Kurs-id"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				id, err := args.ReqInt("courseid")
				if err != nil {
					return nil, err
				}
				return s.CourseInfo(id)
			}),
		mread("moodle_participants", "Teilnehmende eines Kurses mit Rollen (z.B. Dozenten finden, userid für Nachrichten).",
			[]Param{{Name: "courseid", Type: "integer", Required: true, Desc: "Kurs-id"}, {Name: "role", Desc: "Rollenfilter, z.B. 'teacher' oder 'student'"}, {Name: "query", Desc: "Namensfilter"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				id, err := args.ReqInt("courseid")
				if err != nil {
					return nil, err
				}
				return s.Participants(id, args.Str("role"), args.Str("query"))
			}),
		mread("moodle_completion", "Aktivitätsabschluss in einem Kurs: was ist erledigt, was ist noch offen.",
			[]Param{{Name: "courseid", Type: "integer", Required: true, Desc: "Kurs-id"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				id, err := args.ReqInt("courseid")
				if err != nil {
					return nil, err
				}
				return s.Completion(id)
			}),
		mread("moodle_quizzes", "Tests/Quizze mit Zeitfenster, Zeitlimit und erlaubten Versuchen. Mit courseid zusätzlich eigene Versuche und beste Note.",
			[]Param{{Name: "courseid", Type: "integer", Desc: "Kurs-id (für Versuche/Noten)"}, {Name: "only_open", Type: "boolean", Desc: "Nur noch offene, Standard true"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				cid, err := args.IntPtr("courseid")
				if err != nil {
					return nil, err
				}
				return s.Quizzes(cid, args.Bool("only_open", true))
			}),
		mread("moodle_quiz_review", "Auswertung eines abgeschlossenen Quiz-Versuchs: Fragen, eigene Antworten, richtige Lösungen, Punkte, Feedback. Ideal zum Lernen.",
			[]Param{{Name: "attemptid", Type: "integer", Required: true, Desc: "attemptid aus moodle_quizzes"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				id, err := args.ReqInt("attemptid")
				if err != nil {
					return nil, err
				}
				return s.QuizReview(id)
			}),
		mread("moodle_scheduler", "Terminplaner (mod/scheduler, z.B. mündliche Prüfungen): eigener gebuchter Termin mit Datum, Uhrzeit, Dauer, Lehrkraft, Ort und Notizen, dazu vergangene und noch buchbare Zeitfenster. Nur lesen, kein Buchen/Stornieren.",
			[]Param{{Name: "cmid", Type: "integer", Required: true, Desc: "Modul-id der Aktivität (cmid aus moodle_course_contents bzw. ?id= in …/mod/scheduler/view.php)"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				id, err := args.ReqInt("cmid")
				if err != nil {
					return nil, err
				}
				return s.Scheduler(id)
			}),
		mwrite("moodle_scheduler_book", "Bucht ein freies Zeitfenster eines Moodle-Terminplaners (mod/scheduler). Vorschau zeigt Request und Slot; slotid aus moodle_scheduler (available, bookable). Prüft Limit/Belegung vorab.",
			schedulerParams("slotid des freien Zeitfensters (moodle_scheduler → available)"),
			func(s *moodle.Service, a *app.App, args Args, confirm bool) (any, error) {
				cmid, slot, err := schedulerIDs(args)
				if err != nil {
					return nil, err
				}
				return s.SchedulerBook(cmid, slot, confirm)
			}),
		mwrite("moodle_scheduler_cancel", "Storniert eine eigene Buchung in einem Moodle-Terminplaner (mod/scheduler). Vorschau zeigt Request und Slot; slotid aus moodle_scheduler (booked).",
			schedulerParams("slotid der eigenen Buchung (moodle_scheduler → booked)"),
			func(s *moodle.Service, a *app.App, args Args, confirm bool) (any, error) {
				cmid, slot, err := schedulerIDs(args)
				if err != nil {
					return nil, err
				}
				return s.SchedulerCancel(cmid, slot, confirm)
			}),
		mread("moodle_choices", "Abstimmungen und Gruppenwahlen eines Kurses mit Optionen, Belegung und eigener Wahl.",
			[]Param{{Name: "courseid", Type: "integer", Required: true, Desc: "Kurs-id"}},
			func(s *moodle.Service, a *app.App, args Args) (any, error) {
				id, err := args.ReqInt("courseid")
				if err != nil {
					return nil, err
				}
				return s.Choices(id)
			}),
		mwrite("moodle_mark_notifications_read", "Markiert alle Moodle-Benachrichtigungen als gelesen.", nil,
			func(s *moodle.Service, a *app.App, args Args, confirm bool) (any, error) {
				if !confirm {
					return map[string]any{"action": "Alle Moodle-Benachrichtigungen als gelesen markieren"}, nil
				}
				return s.MarkNotificationsRead()
			}),
		mwrite("moodle_mark_messages_read", "Markiert alle ungelesenen Moodle-Unterhaltungen als gelesen (Vorschau nennt sie).", nil,
			func(s *moodle.Service, a *app.App, args Args, confirm bool) (any, error) {
				return s.MarkMessagesRead(confirm)
			}),
		mwrite("moodle_forum_reply", "Antwortet auf einen Forenbeitrag.",
			[]Param{{Name: "postid", Type: "integer", Required: true, Desc: "postid aus moodle_forum_posts"}, {Name: "message", Required: true, Desc: "Text (Zeilenumbrüche werden übernommen)"},
				{Name: "subject", Desc: "Betreff, Standard 'Re: …'"}},
			func(s *moodle.Service, a *app.App, args Args, confirm bool) (any, error) {
				id, err := args.ReqInt("postid")
				if err != nil {
					return nil, err
				}
				return s.ForumReply(id, args.Str("subject"), args.Str("message"), confirm)
			}),
		mwrite("moodle_forum_post", "Startet eine neue Diskussion in einem Forum (für alle sichtbar).",
			[]Param{{Name: "forumid", Type: "integer", Required: true, Desc: "forumid aus moodle_forum_discussions"}, {Name: "subject", Required: true, Desc: "Betreff"},
				{Name: "message", Required: true, Desc: "Text"}},
			func(s *moodle.Service, a *app.App, args Args, confirm bool) (any, error) {
				id, err := args.ReqInt("forumid")
				if err != nil {
					return nil, err
				}
				return s.ForumPost(id, args.Str("subject"), args.Str("message"), confirm)
			}),
		mwrite("moodle_send_message", "Sendet eine Moodle-Nachricht an eine Unterhaltung oder einen Nutzer.",
			[]Param{{Name: "conversationid", Type: "integer", Desc: "aus moodle_conversations"}, {Name: "userid", Type: "integer", Desc: "Empfänger, z.B. aus moodle_participants"},
				{Name: "text", Required: true, Desc: "Nachricht"}},
			func(s *moodle.Service, a *app.App, args Args, confirm bool) (any, error) {
				cid, err := args.IntPtr("conversationid")
				if err != nil {
					return nil, err
				}
				uid, err := args.IntPtr("userid")
				if err != nil {
					return nil, err
				}
				return s.SendMessage(cid, uid, args.Str("text"), confirm)
			}),
		mwrite("moodle_choice_submit", "Gibt eine Stimme in einer Abstimmung ab (ersetzt eine vorherige Wahl).",
			[]Param{{Name: "choiceid", Type: "integer", Required: true, Desc: "aus moodle_choices"}, {Name: "optionids", Type: "array:integer", Required: true, Desc: "Gewählte optionid(s)"}},
			func(s *moodle.Service, a *app.App, args Args, confirm bool) (any, error) {
				id, err := args.ReqInt("choiceid")
				if err != nil {
					return nil, err
				}
				opts, err := args.Ints("optionids")
				if err != nil {
					return nil, err
				}
				return s.ChoiceSubmit(id, opts, confirm)
			}),
		mwrite("moodle_assignment_submit", "Lädt lokale Dateien und/oder Online-Text als Abgabe hoch, optional direkt zur Bewertung einreichen. ERSETZT bisher abgegebene Dateien (Vorschau zeigt, was ersetzt würde).",
			[]Param{{Name: "assignid", Type: "integer", Required: true, Desc: "aus moodle_assignments"}, {Name: "files", Type: "array:string", Desc: "Lokale Dateipfade"},
				{Name: "online_text", Desc: "Text für Online-Abgaben"}, {Name: "submit_for_grading", Type: "boolean", Desc: "Endgültig einreichen, Standard false (bleibt Entwurf)"}},
			func(s *moodle.Service, a *app.App, args Args, confirm bool) (any, error) {
				id, err := args.ReqInt("assignid")
				if err != nil {
					return nil, err
				}
				return s.AssignmentSubmit(id, args.Strs("files"), args.Str("online_text"), args.Bool("submit_for_grading", false), confirm)
			}),
		{Name: "moodle_call", Kind: Read, MayWrite: true, Desc: "Beliebige Moodle-Webservice-Funktion aufrufen (Fallback). Parameter als JSON-Objekt. Nur lesende Funktionen (get_/search_/list_ …) laufen direkt; alles andere ändert Daten und braucht confirm=true – vorher ausdrücklich den Nutzer fragen.",
			Params: []Param{{Name: "wsfunction", Required: true, Desc: "z.B. core_course_get_categories"}, {Name: "params", Type: "object", Desc: "Parameter der Funktion"},
				{Name: "confirm", Type: "boolean", Desc: "Pflicht für schreibende Funktionen"}},
			Run: func(a *app.App, args Args) (any, error) {
				s, err := ms(a)
				if err != nil {
					return nil, err
				}
				p, err := args.Map("params")
				if err != nil {
					return nil, err
				}
				return s.RawCall(args.Str("wsfunction"), p, args.Bool("confirm", false))
			}},
	}
}

func schedulerParams(slotDesc string) []Param {
	return []Param{{Name: "cmid", Type: "integer", Required: true, Desc: "Modul-id des Terminplaners (cmid)"},
		{Name: "slotid", Type: "integer", Required: true, Desc: slotDesc}}
}

func schedulerIDs(args Args) (cmid, slotid int64, err error) {
	if cmid, err = args.ReqInt("cmid"); err != nil {
		return
	}
	slotid, err = args.ReqInt("slotid")
	return
}
