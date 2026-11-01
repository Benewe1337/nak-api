package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Raindancer118/nak-api/internal/app"
	"github.com/Raindancer118/nak-api/internal/client"
	"github.com/Raindancer118/nak-api/internal/drift"
	"github.com/Raindancer118/nak-api/internal/moodle"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func fixture(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type hit struct{ Method, Path, Action string }

// fakeCIS routes paths (+ TYPO3 action) to the anonymised fixtures.
type fakeCIS struct {
	mu   sync.Mutex
	hits []hit
}

func (f *fakeCIS) handler(t *testing.T) http.HandlerFunc {
	stundenplaene := `<html><body><main><ul class="ce-uploads"><li><a href="/fileadmin/Infos/Stundenplaene/I23a_6.ics"><span class="ce-uploads-fileName">I23a_6.ics</span></a></li></ul></main><a href="/login/?logintype=logout">x</a></body></html>`
	return func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		act := ""
		for k, v := range r.Form {
			if strings.HasSuffix(k, "[action]") {
				act = v[0]
			}
		}
		if act == "" {
			act = htmlAction(r.URL.RawQuery)
		}
		f.mu.Lock()
		f.hits = append(f.hits, hit{r.Method, r.URL.Path, act})
		f.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: client.SessionCookie, Value: "s", Path: "/"})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page := func(rel string) { io.WriteString(w, fixture(t, rel)) }
		switch p := r.URL.Path; {
		case p == "/":
			page("planning/testdata/startseite.html")
		case p == "/mein-profil/meine-daten":
			page("profile/testdata/meine-daten.html")
		case strings.HasSuffix(p, "/e-mails-verwalten"):
			page("profile/testdata/emails.html")
		case strings.HasSuffix(p, "/telefonnummern-verwalten"):
			page("profile/testdata/telefon.html")
		case strings.HasSuffix(p, "/adressen-verwalten"):
			page("profile/testdata/adressen.html")
		case p == "/mein-profil/notenfreigabe":
			page("profile/testdata/notenfreigabe.html")
		case strings.HasSuffix(p, "/datenfreigabe-verwalten"):
			page("profile/testdata/datenfreigabe.html")
		case strings.HasSuffix(p, "/standard-titel"):
			page("profile/testdata/zahlung.html")
		case strings.HasSuffix(p, "/meine-kommilitonen"):
			page("profile/testdata/kommilitonen.html")
		case strings.HasSuffix(p, "/guthaben"):
			page("profile/testdata/guthaben.html")
		case strings.HasSuffix(p, "/vertiefungsrichtungen"):
			page("profile/testdata/vertiefung.html")
		case strings.HasSuffix(p, "/leistungsuebersicht"):
			switch act {
			case "statistic":
				page("grades/testdata/statistic.html")
			case "anwesenheiten":
				page("grades/testdata/anwesenheiten.html")
			default:
				page("grades/testdata/leistungsuebersicht.html")
			}
		case strings.HasSuffix(p, "/meine-pruefungen"):
			page("exams/testdata/meine-pruefungen.html")
		case strings.HasSuffix(p, "/mein-studienplan"):
			page("planning/testdata/studienplan.html")
		case strings.HasSuffix(p, "/vorlesungszeiten"):
			page("planning/testdata/vorlesungszeiten.html")
		case p == "/studium/bachelor/stundenplaene":
			io.WriteString(w, stundenplaene)
		case strings.HasSuffix(p, ".ics"):
			w.Header().Set("Content-Type", "text/calendar")
			page("timetable/testdata/zenturie.ics")
		case p == "/studium/bachelor/seminare":
			switch act {
			case "personalList":
				page("seminars/testdata/meine.html")
			case "show":
				page("seminars/testdata/detail.html")
			case "showParticipantList", "showWaitList":
				page("seminars/testdata/teilnehmer.html")
			default:
				page("seminars/testdata/liste.html")
			}
		case strings.HasSuffix(p, "/wahlpflichtkurse-waehlen"):
			if r.Method == http.MethodPost {
				page("wahlpflicht/testdata/detail-wahl-offen.html")
			} else {
				page("wahlpflicht/testdata/liste-geschlossen.html")
			}
		case p == "/studium/bachelor/transferleistungen-praxisberichte":
			switch act {
			case "performance":
				page("transfer/testdata/bewertung.html")
			case "new":
				page("transfer/testdata/beantragen.html")
			default:
				page("transfer/testdata/liste-live.html")
			}
		case strings.HasSuffix(p, "/online-bescheinigungen"):
			if act == "document" {
				w.Header().Set("Content-Type", "application/pdf")
				io.WriteString(w, "%PDF-1.4 cert")
				return
			}
			page("certs/testdata/bescheinigungen.html")
		case p == "/studium/pruefungen/pruefungsplan":
			page("pages/testdata/pruefungsplan.html")
		default:
			w.WriteHeader(404)
		}
	}
}

func htmlAction(rawQuery string) string {
	q, _ := url.ParseQuery(rawQuery)
	for k, v := range q {
		if strings.HasSuffix(k, "[action]") {
			return v[0]
		}
	}
	return ""
}

// fakeMoodleJSON answers the web service functions the cross tools use.
func fakeMoodle(t *testing.T, now int64) *httptest.Server {
	resp := map[string]string{
		"core_webservice_get_site_info": `{"userid":4711,"username":"10000","fullname":"Max Mustermann"}`,
		"core_enrol_get_users_courses":  `[{"id":2,"shortname":"I151_I23a","fullname":"I151 - Softwaretechnik","startdate":1780000000,"enddate":1800000000}]`,
		"core_calendar_get_action_events_by_timesort": `{"events":[{"name":"Abgabe Projekt","activityname":"Projekt","modulename":"assign","timesort":` +
			jsonInt(now+2*86400) + `,"course":{"id":2,"shortname":"I151_I23a"},"action":{"name":"Abgabe hinzufügen","actionable":true}}]}`,
		"message_popup_get_unread_popup_notification_count": "2",
		"core_message_get_unread_conversations_count":       "0",
		"mod_assign_get_assignments":                        `{"courses":[]}`,
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/login/token.php") {
			io.WriteString(w, `{"token":"t"}`)
			return
		}
		if b, ok := resp[r.PostForm.Get("wsfunction")]; ok {
			io.WriteString(w, b)
			return
		}
		io.WriteString(w, `{"exception":"x","errorcode":"accessexception","message":"no"}`)
	}))
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

type env struct {
	app     *app.App
	cis     *fakeCIS
	session string
}

func newEnv(t *testing.T) *env {
	f := &fakeCIS{}
	cisSrv := httptest.NewServer(f.handler(t))
	t.Cleanup(cisSrv.Close)
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.Local)
	mSrv := fakeMoodle(t, now.Unix())
	t.Cleanup(mSrv.Close)

	a := app.FromEnv()
	berlin, _ := time.LoadLocation("Europe/Berlin")
	a.Zone, a.Now = berlin, func() time.Time { return now }
	a.DownloadDir = t.TempDir()
	dir := t.TempDir()
	c, _ := client.NewWith(client.Options{BaseURL: cisSrv.URL, SessionDir: dir})
	c.Relogin = func(c *client.Client) error { _, err := c.GetRaw("/"); return err }
	a.UseCIS(c)
	mc := moodle.NewClient(mSrv.URL, "u", "p")
	mc.AuditDir = dir
	ms := moodle.NewService(mc, berlin)
	ms.Now = a.Now
	a.UseMoodle(ms)
	return &env{app: a, cis: f, session: dir}
}

func (e *env) writes() string {
	b, _ := os.ReadFile(filepath.Join(e.session, "writes.log"))
	return string(b)
}

func startMCP(t *testing.T, a *app.App) *mcpclient.Client {
	s := server.NewMCPServer("nak-test", "test", server.WithToolCapabilities(false))
	All().Register(s, a)
	c, err := mcpclient.NewInProcessClient(s)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1"}
	if _, err := c.Initialize(ctx, req); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func call(t *testing.T, c *mcpclient.Client, name string, args map[string]any) (string, bool) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name, req.Params.Arguments = name, args
	res, err := c.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var sb strings.Builder
	for _, ct := range res.Content {
		if tc, ok := ct.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), res.IsError
}

func TestStartupListsAllToolsWithSafeSchemas(t *testing.T) {
	e := newEnv(t)
	c := startMCP(t, e.app)
	res, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	reg := All()
	if len(res.Tools) != len(reg.All()) || len(res.Tools) < 75 {
		t.Fatalf("tools = %d (registry %d)", len(res.Tools), len(reg.All()))
	}
	prefixes := map[string]int{}
	for _, tool := range res.Tools {
		prefixes[strings.SplitN(tool.Name, "_", 2)[0]]++
		rt := reg.Get(tool.Name)
		if rt.Kind != Write {
			continue
		}
		if _, ok := tool.InputSchema.Properties["confirm"]; !ok {
			t.Errorf("%s: write tool without confirm parameter", tool.Name)
		}
		if tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint || *tool.Annotations.ReadOnlyHint {
			t.Errorf("%s: write tool must be annotated destructive", tool.Name)
		}
		if !strings.HasPrefix(tool.Description, "⚠") {
			t.Errorf("%s: description must warn", tool.Name)
		}
	}
	if prefixes["cis"] < 40 || prefixes["moodle"] != 32 || prefixes["nak"] != 12 || prefixes["eduvault"] != 3 {
		t.Errorf("prefixes = %v", prefixes)
	}
}

func TestReadToolsAgainstFakeCIS(t *testing.T) {
	e := newEnv(t)
	c := startMCP(t, e.app)
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"cis_status", nil, `"zenturie": "I23a"`},
		{"cis_profile", nil, "Musterweg 1"},
		{"cis_contact", nil, "max@example.org"},
		{"cis_address", nil, "Hauptadresse"},
		{"cis_sharing", nil, "noten_fuer_betrieb_freigegeben"},
		{"cis_payment", nil, "sepa_mandat_erteilt"},
		{"cis_classmates", nil, "Beispiel GmbH"},
		{"cis_balance", nil, "0,00 €"},
		{"cis_vertiefung", nil, "E-Commerce"},
		{"cis_grades", map[string]any{"status": "offen"}, "Softwaretechnik"},
		{"cis_grade_distribution", map[string]any{"module_nr": "I140"}, `"percentile"`},
		{"cis_attendance", map[string]any{"module_nr": "I140"}, "Teilgenommen"},
		{"cis_studienplan", map[string]any{"semester": 6}, "Softwaretechnik"},
		{"cis_progress", nil, "thesis_requirements"},
		{"cis_timetable", map[string]any{"from": "2026-07-27", "days": 14, "all_wpf": true}, "Softwaretechnik"},
		{"cis_vorlesungszeiten", nil, "III/26"},
		{"cis_abschlussfristen", nil, "März 2027"},
		{"cis_list_klausuren", map[string]any{"filter": "registered"}, "12260"},
		{"cis_list_seminars", map[string]any{"available_only": true}, "subscribeToSeminar"},
		{"cis_seminar_detail", map[string]any{"seminar_id": "10606"}, `"kreditpunkte": "1"`},
		{"cis_seminar_participation", map[string]any{"seminar_id": "10606"}, `"participants"`},
		{"cis_list_wahlpflicht", nil, "KI und Management"},
		{"cis_wahlpflicht_detail", map[string]any{"module_id": "1657"}, "9532"},
		{"cis_list_transfer", nil, "IT-Organisation"},
		{"cis_transfer_bewertung", map[string]any{"transfer_id": "14534"}, "kriterien"},
		{"cis_transfer_options", nil, "bericht_nr"},
		{"cis_list_certs", nil, "WS 2026"},
		{"cis_download_cert", map[string]any{"semester": "WS 2026", "lang": "en"}, `"bytes"`},
		{"cis_page", map[string]any{"path": "/studium/pruefungen/pruefungsplan"}, "Wirtschaftsinformatik"},
		{"nak_deadlines", map[string]any{"days": 30}, `"what": "Projekt"`},
		{"nak_agenda", map[string]any{"from": "2026-09-20", "days": 14}, "Moodle-Frist"},
		{"nak_dashboard", nil, "moodle_unread"},
		{"nak_module", map[string]any{"module_nr": "I151"}, "moodle_courses"},
	}
	for _, tc := range cases {
		out, isErr := call(t, c, tc.tool, tc.args)
		if isErr || !strings.Contains(out, tc.want) {
			t.Errorf("%s: err=%v, want %q in\n%.600s", tc.tool, isErr, tc.want, out)
		}
	}
	if w := e.writes(); w != "" {
		t.Fatalf("read tools wrote: %s", w)
	}
}

func TestDeadlinesCombineBothSystems(t *testing.T) {
	e := newEnv(t)
	out, isErr := call(t, startMCP(t, e.app), "nak_deadlines", map[string]any{"days": 30})
	if isErr {
		t.Fatal(out)
	}
	for _, want := range []string{`"source": "moodle"`, `"kind": "exam"`, `"kind": "exam_deregistration"`, `"kind": "exam_registration"`, "Letzte Abmeldemöglichkeit"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%.1500s", want, out)
		}
	}
	if strings.Contains(out, "unavailable_sources") {
		t.Errorf("a source failed:\n%s", out)
	}
}

// The central safety property: no write tool sends anything without confirm.
func TestNoWriteWithoutConfirm(t *testing.T) {
	e := newEnv(t)
	c := startMCP(t, e.app)
	pdf := filepath.Join(t.TempDir(), "auftrag.pdf")
	os.WriteFile(pdf, []byte("%PDF-1.4 x"), 0o600)
	upload := filepath.Join(t.TempDir(), "abgabe.txt")
	os.WriteFile(upload, []byte("x"), 0o600)
	args := map[string]map[string]any{
		"cis_klausur_action":             {"exam_id": "12259", "action": "register"},
		"cis_seminar_action":             {"seminar_id": "10606"},
		"cis_select_wahlpflicht":         {"module_id": "1657", "termin_id": "9532"},
		"cis_transfer_register":          {"no": "5", "topic": "Thema", "module": "358", "file_path": pdf},
		"cis_update_contact":             {"email": "neu@example.org", "mobile": "0170-1"},
		"cis_update_address":             {"strasse": "Neu 1"},
		"cis_set_sharing":                {"noten_betrieb": false, "kommilitonen": map[string]any{"Adresse": "1"}},
		"cis_set_vertiefung":             {"value": "8"},
		"moodle_mark_notifications_read": {},
		"moodle_mark_messages_read":      {},
		"moodle_send_message":            {"conversationid": 1, "text": "hi"},
		"moodle_forum_reply":             {"postid": 1, "message": "x"},
		"moodle_forum_post":              {"forumid": 1, "subject": "s", "message": "m"},
		"moodle_choice_submit":           {"choiceid": 1, "optionids": []any{1}},
		"moodle_assignment_submit":       {"assignid": 1, "files": []any{upload}},
		"nak_report_drift":               {},
	}
	for _, tool := range All().All() {
		if tool.Kind != Write {
			continue
		}
		a, ok := args[tool.Name]
		if !ok {
			t.Errorf("no safety test case for write tool %s", tool.Name)
			continue
		}
		out, _ := call(t, c, tool.Name, a)
		if strings.Contains(out, `"mode": "executed"`) {
			t.Errorf("%s executed without confirm: %s", tool.Name, out)
		}
	}
	if w := e.writes(); w != "" {
		t.Fatalf("writes without confirm:\n%s", w)
	}
	for _, h := range e.cis.hits {
		switch h.Action {
		case "register", "deregister", "subscribeToSeminar", "anWartelisteAnmelden", "order", "confirmNew", "create",
			"email", "telefon", "address", "betriebsfreigabe", "submit", "handleForm":
			t.Errorf("write-like request reached the fake CIS: %+v", h)
		}
	}
	// Previews of CIS writes must show what would happen.
	out, _ := call(t, c, "cis_klausur_action", args["cis_klausur_action"])
	if !strings.Contains(out, `"mode": "preview"`) || !strings.Contains(out, "Diskrete Mathematik 2") || !strings.Contains(out, "NICHTS wurde gesendet") {
		t.Errorf("exam preview:\n%s", out)
	}
}

func TestConfirmedWriteReachesOnlyTheFake(t *testing.T) {
	e := newEnv(t)
	c := startMCP(t, e.app)
	out, isErr := call(t, c, "cis_klausur_action", map[string]any{"exam_id": "12259", "action": "register", "confirm": true})
	if isErr || !strings.Contains(out, `"mode": "executed"`) {
		t.Fatalf("confirmed: %s", out)
	}
	found := false
	for _, h := range e.cis.hits {
		if h.Action == "register" && h.Method == http.MethodGet {
			found = true
		}
	}
	if !found || !strings.Contains(e.writes(), "cis GET") {
		t.Errorf("register request missing: hits=%v writes=%q", e.cis.hits, e.writes())
	}
	// Mismatched action is refused even with confirm.
	if out, isErr := call(t, c, "cis_klausur_action", map[string]any{"exam_id": "12260", "action": "register", "confirm": true}); !isErr {
		t.Errorf("mismatched action accepted: %s", out)
	}
}

func TestReadOnlyModeBlocksConfirmedWrites(t *testing.T) {
	e := newEnv(t)
	cc, _ := e.app.CISClient()
	cc.ReadOnly = true
	m, _ := e.app.Moodle()
	m.C.ReadOnly = true
	c := startMCP(t, e.app)
	if out, isErr := call(t, c, "cis_klausur_action", map[string]any{"exam_id": "12259", "action": "register", "confirm": true}); !isErr || !strings.Contains(out, "read-only") {
		t.Errorf("cis: %s", out)
	}
	if out, isErr := call(t, c, "moodle_mark_notifications_read", map[string]any{"confirm": true}); !isErr || !strings.Contains(out, "read-only") {
		t.Errorf("moodle: %s", out)
	}
	for _, h := range e.cis.hits {
		if h.Action == "register" {
			t.Fatal("register reached the server in read-only mode")
		}
	}
}

func TestArgCoercionAndValidation(t *testing.T) {
	e := newEnv(t)
	c := startMCP(t, e.app)
	if out, isErr := call(t, c, "cis_grade_distribution", nil); !isErr || !strings.Contains(out, "module_nr is required") {
		t.Errorf("missing arg: %s", out)
	}
	if out, isErr := call(t, c, "cis_studienplan", map[string]any{"semester": "6"}); isErr || !strings.Contains(out, `"semester": 6`) {
		t.Errorf("string number: %.200s", out)
	}
	if out, isErr := call(t, c, "cis_download", map[string]any{"url": "/mein-profil/meine-planung/meine-pruefungen?tx_naexams_naexams%5Baction%5D=register&cHash=1"}); !isErr {
		t.Errorf("action link downloadable: %s", out)
	}
	if out, isErr := call(t, c, "cis_page", map[string]any{"path": "/x?tx_naexams_naexams%5Baction%5D=register"}); !isErr {
		t.Errorf("action link readable: %s", out)
	}
}

func TestSelfcheckAgainstFixturesAndDriftHint(t *testing.T) {
	e := newEnv(t)
	c := startMCP(t, e.app)
	out, isErr := call(t, c, "nak_selfcheck", nil)
	if isErr || !strings.Contains(out, `"checks"`) {
		t.Fatalf("selfcheck: %s", out)
	}
	if strings.Contains(out, `"drift"`) {
		t.Errorf("fixtures must not drift:\n%s", out)
	}
	// A changed page yields a drift error with the reporting hint.
	err := DriftHint(fmt.Errorf("x: %w", drift.New("/p", "table", "<main><table><tr><th>A</th></tr></table></main>")))
	if !strings.Contains(err, "nak_report_drift") || !strings.Contains(err, "github.com/Raindancer118/nak-api/issues/new") {
		t.Errorf("hint = %s", err)
	}
}
