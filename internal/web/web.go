// Package web is the browser front end of nak: a single-user HTTP server
// that exposes the tool registry as a JSON API next to the embedded UI.
// One owner, one token — there are no accounts.
package web

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Raindancer118/nak-api/internal/app"
	"github.com/Raindancer118/nak-api/internal/demo"
	"github.com/Raindancer118/nak-api/internal/drift"
	"github.com/Raindancer118/nak-api/internal/tools"
)

//go:embed ui
var uiFS embed.FS

const (
	cookieName       = "nak_session"
	confirmHeader    = "X-Nak-Confirm"
	tokenFile        = "web-token"
	maxLoginFailures = 10
	loginWindow      = 15 * time.Minute
	maxBody          = 1 << 20
)

type Config struct {
	Token    string
	CacheTTL time.Duration
	Version  string
	Now      func() time.Time
	// UIDir serves the UI from disk instead of the embedded copy (UI work
	// without rebuilding).
	UIDir string
	// CacheFile keeps tool results across restarts ("" = memory only).
	CacheFile string
	// Demo needs no login (invented data only) and never asks the CIS.
	Demo bool
	// Operator runs this instance for someone else (a friend's instance): the
	// login page and the settings say who, what is stored, how to delete it.
	Operator, OperatorContact string
}

// SaveCache writes pending cache changes to CacheFile (call on shutdown).
func (s *Server) SaveCache() error { return s.store.save() }

type Server struct {
	app *app.App
	reg *tools.Registry
	cfg Config
	ui  fs.FS

	store   *store
	tokenMu sync.RWMutex // the access key rotates on an instance reset
	watch   *watcher
	history *history
	stats   *stats

	versions   versionCache
	releaseURL string // tests point this at a fake GitHub

	loginMu sync.Mutex
	logins  map[string]*attempts
	login   *template.Template
}

type attempts struct {
	fails int
	first time.Time
}

func New(a *app.App, reg *tools.Registry, cfg Config) *Server {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	ui, err := fs.Sub(uiFS, "ui")
	if err != nil {
		panic(err)
	}
	if cfg.UIDir != "" {
		ui = os.DirFS(cfg.UIDir)
	}
	s := &Server{
		app: a, reg: reg, cfg: cfg, ui: ui,
		store:  newStore(cfg.CacheFile),
		logins: map[string]*attempts{},
	}
	if cfg.UIDir == "" {
		s.login = template.Must(template.ParseFS(ui, "login.html"))
	}
	histFile := ""
	if a.ConfigDir != "" && cfg.CacheFile != "" {
		histFile = filepath.Join(a.ConfigDir, "history.json")
	}
	s.history = newHistory(histFile, cfg.Now)
	if cfg.Demo {
		b, _ := json.Marshal(demo.WrittenExam(cfg.Now()))
		s.history.observe("cis_list_klausuren", b)
	}
	s.stats = newStats(cfg.Now())
	s.watch = newWatcher(s)
	return s
}

// loginTemplate re-reads login.html on every request when serving from disk.
func (s *Server) loginTemplate() (*template.Template, error) {
	if s.login != nil {
		return s.login, nil
	}
	return template.ParseFS(s.ui, "login.html")
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginPost)
	mux.HandleFunc("POST /logout", s.logout)
	mux.Handle("GET /api/tools", s.authed(http.HandlerFunc(s.listTools)))
	mux.Handle("POST /api/tools/{name}", s.authed(http.HandlerFunc(s.callTool)))
	mux.Handle("GET /files/{path...}", s.authed(http.HandlerFunc(s.file)))
	mux.Handle("GET /api/settings", s.authed(http.HandlerFunc(s.getSettings)))
	mux.Handle("GET /api/calendar", s.authed(http.HandlerFunc(s.calendarInfo)))
	mux.Handle("GET /api/history", s.authed(http.HandlerFunc(s.historyAPI)))
	mux.Handle("GET /api/stats", s.authed(http.HandlerFunc(s.statsAPI)))
	mux.Handle("GET /api/export", s.authed(http.HandlerFunc(s.export)))
	mux.Handle("GET /api/version", s.authed(http.HandlerFunc(s.version)))
	mux.Handle("POST /api/reset", s.notInDemo(s.authed(s.jsonOnly(s.reset))))
	mux.Handle("POST /api/sessions/revoke", s.notInDemo(s.authed(s.jsonOnly(s.revokeSessions))))
	mux.Handle("GET /metrics", s.authed(http.HandlerFunc(s.metrics)))
	mux.Handle("POST /api/upload", s.notInDemo(s.authed(s.sameOriginOnly(s.upload))))
	mux.Handle("GET /api/notifications", s.authed(http.HandlerFunc(s.notifications)))
	mux.Handle("POST /api/notifications/read", s.authed(s.jsonOnly(s.notificationsRead)))
	mux.Handle("GET /api/events", s.authed(http.HandlerFunc(s.events)))
	mux.Handle("PUT /api/settings/notify", s.notInDemo(s.authed(s.jsonOnly(s.putNotify))))
	mux.Handle("PUT /api/settings/nav", s.notInDemo(s.authed(s.jsonOnly(s.putNav))))
	mux.Handle("PUT /api/settings/home", s.notInDemo(s.authed(s.jsonOnly(s.putHome))))
	mux.Handle("PUT /api/settings/ui", s.notInDemo(s.authed(s.jsonOnly(s.putUI))))
	mux.Handle("PUT /api/settings/top", s.notInDemo(s.authed(s.jsonOnly(s.putTop))))
	mux.Handle("GET /api/grades/pending", s.authed(http.HandlerFunc(s.pendingAPI)))
	mux.Handle("POST /api/calendar/rotate", s.notInDemo(s.authed(s.jsonOnly(s.calendarRotate))))
	mux.HandleFunc("GET /calendar/{file}", s.calendarFeed)
	mux.Handle("PUT /api/settings/eduvault", s.notInDemo(s.authed(s.jsonOnly(s.putEduVault))))
	mux.Handle("DELETE /api/settings/eduvault", s.notInDemo(s.authed(s.sameOriginOnly(s.deleteEduVault))))
	mux.Handle("POST /api/cache/clear", s.authed(s.jsonOnly(func(w http.ResponseWriter, r *http.Request) {
		s.store.clear()
		writeJSON(w, http.StatusOK, map[string]any{"cleared": true})
	})))
	assets := http.FileServerFS(s.ui)
	mux.Handle("GET /assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// revalidate on every load (cheap: Last-Modified), so an update or a
		// UI edit shows up at once
		w.Header().Set("Cache-Control", "no-cache")
		assets.ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET /{$}", s.index)
	// the service worker must live at the root to control the whole app
	mux.HandleFunc("GET /sw.js", s.rootFile("assets/sw.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /manifest.webmanifest", s.rootFile("assets/manifest.webmanifest", "application/manifest+json"))
	return s.headers(mux)
}

// ── middleware ─────────────────────────────────────────────────────────────

func secure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// "no-referrer" would make browsers send Origin: null on our own form posts
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if secure(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) token() string {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	return s.cfg.Token
}

func (s *Server) sessionValue() string {
	m := hmac.New(sha256.New, []byte(s.token()))
	m.Write([]byte("nak-web-session-v1"))
	return hex.EncodeToString(m.Sum(nil))
}

func eq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func (s *Server) isAuthed(r *http.Request) bool {
	if s.cfg.Demo {
		return true
	}
	if b, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return eq(strings.TrimSpace(b), s.token())
	}
	c, err := r.Cookie(cookieName)
	return err == nil && eq(c.Value, s.sessionValue())
}

// notInDemo blocks what a public demo must not do: reach out to other
// servers (EduVault, ntfy), take uploads, or change what the next visitor sees.
func (s *Server) notInDemo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Demo {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "In der Demo nicht verfügbar."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.isAuthed(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "nicht angemeldet"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin rejects cross-site requests. The session cookie is SameSite=Strict
// already; this also covers browsers that send Origin but no Sec-Fetch-Site.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "cross-site", "same-site":
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// ── pages ──────────────────────────────────────────────────────────────────

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": s.cfg.Version})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthed(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !s.cfg.Demo && r.Header.Get("Authorization") == "" {
		s.setSession(w, r)
	}
	b, err := fs.ReadFile(s.ui, "index.html")
	if err != nil {
		http.Error(w, "UI missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}

func (s *Server) rootFile(name, ctype string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(s.ui, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	}
}

// clientIP is the peer address; behind a reverse proxy (NAK_TRUST_PROXY=1)
// the first X-Forwarded-For entry, so one client's failed logins do not lock
// out everybody coming through the same proxy. Without the switch the header
// is ignored — anyone could send it.
func clientIP(r *http.Request) string {
	if os.Getenv("NAK_TRUST_PROXY") == "1" {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if ip := strings.TrimSpace(strings.Split(xff, ",")[0]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) locked(ip string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	a := s.logins[ip]
	if a == nil {
		return false
	}
	if s.cfg.Now().Sub(a.first) > loginWindow {
		delete(s.logins, ip)
		return false
	}
	return a.fails >= maxLoginFailures
}

func (s *Server) failed(ip string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	a := s.logins[ip]
	if a == nil {
		a = &attempts{first: s.cfg.Now()}
		s.logins[ip] = a
	}
	a.fails++
}

// tryLogin answers the login attempt completely (cookie + redirect or error page).
func (s *Server) tryLogin(w http.ResponseWriter, r *http.Request, tok string) {
	ip := clientIP(r)
	if s.locked(ip) {
		s.renderLogin(w, r, http.StatusTooManyRequests, "Zu viele Fehlversuche. Bitte in ein paar Minuten erneut versuchen.")
		return
	}
	if !eq(strings.TrimSpace(tok), s.token()) {
		s.failed(ip)
		s.renderLogin(w, r, http.StatusUnauthorized, "Der Zugangsschlüssel stimmt nicht.")
		return
	}
	s.loggedIn(w, r, ip)
}

func (s *Server) loggedIn(w http.ResponseWriter, r *http.Request, ip string) {
	s.loginMu.Lock()
	delete(s.logins, ip)
	s.loginMu.Unlock()
	s.setSession(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// setSession (re)issues the session cookie. Every page load calls it, so a
// session only ends after 30 days without a visit.
func (s *Server) setSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: s.sessionValue(), Path: "/",
		MaxAge: 30 * 24 * 3600, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteStrictMode,
	})
}

// loginPage never accepts a token from the query string (it would end up in
// browser history and proxy logs); the log link carries it as #fragment and
// login.js posts it.
func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Demo {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderLogin(w, r, http.StatusOK, "")
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-site request", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if tok := r.PostFormValue("token"); tok != "" {
		s.tryLogin(w, r, tok)
		return
	}
	s.tryAccountLogin(w, r, r.PostFormValue("username"), r.PostFormValue("password"))
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, status int, msg string) {
	var buf bytes.Buffer
	tmpl, err := s.loginTemplate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := map[string]any{"Error": msg, "Version": s.cfg.Version, "FirstRun": s.app.AccountUser() == "",
		"User": strings.TrimSpace(r.PostFormValue("username")), "TokenMode": r.PostFormValue("token") != "" || r.URL.Query().Get("mode") == "token",
		"Operator": s.cfg.Operator, "OperatorContact": s.cfg.OperatorContact}
	if err := tmpl.Execute(&buf, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-site request", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ── API ────────────────────────────────────────────────────────────────────

var kindNames = map[tools.Kind]string{tools.Read: "read", tools.Local: "local", tools.Write: "write"}

func (s *Server) listTools(w http.ResponseWriter, r *http.Request) {
	type param struct {
		Name     string   `json:"name"`
		Type     string   `json:"type"`
		Desc     string   `json:"description"`
		Required bool     `json:"required,omitempty"`
		Enum     []string `json:"enum,omitempty"`
	}
	type tool struct {
		Name   string  `json:"name"`
		Kind   string  `json:"kind"`
		Desc   string  `json:"description"`
		Params []param `json:"params"`
	}
	out := []tool{}
	for _, t := range s.reg.All() {
		e := tool{Name: t.Name, Kind: kindNames[t.Kind], Desc: t.Desc, Params: []param{}}
		for _, p := range t.Params {
			typ := p.Type
			if typ == "" {
				typ = "string"
			}
			e.Params = append(e.Params, param{p.Name, typ, p.Desc, p.Required, p.Enum})
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": out, "read_only": s.app.ReadOnly, "version": s.cfg.Version})
}

func (s *Server) callTool(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-site request"})
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": "Content-Type must be application/json"})
		return
	}
	t := s.reg.Get(r.PathValue("name"))
	if t == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown tool " + r.PathValue("name")})
		return
	}
	args := tools.Args{}
	var err error
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err == nil && len(bytes.TrimSpace(body)) > 0 {
		err = json.Unmarshal(body, &args)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be a JSON object: " + err.Error()})
		return
	}
	for _, p := range t.Params {
		if p.Required && !args.Has(p.Name) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": p.Name + " is required"})
			return
		}
	}
	if t.Kind == tools.Local {
		// Downloads stay in the download dir so /files can serve them (the
		// tools name their target parameter differently).
		for _, k := range []string{"out", "output_path", "target_dir"} {
			delete(args, k)
		}
	}
	if t.Kind == tools.Write && args.Bool("confirm", false) && r.Header.Get(confirmHeader) != "JA" {
		writeJSON(w, http.StatusPreconditionRequired, map[string]any{"error": "verbindliche Aktion: confirm=true braucht zusätzlich den Header " + confirmHeader + ": JA"})
		return
	}

	key := ""
	if (t.Kind == tools.Read && !t.MayWrite) || reusableDownload(t) {
		k, _ := json.Marshal(args) // map keys are sorted
		key = t.Name + " " + string(k)
	}
	q := r.URL.Query()
	fresh, wait := q.Get("fresh") == "1", q.Get("wait") == "1"

	if key != "" && !fresh {
		if e, ok := s.store.get(key); ok && usable(e) {
			age := s.cfg.Now().Sub(e.At)
			switch {
			case t.Kind == tools.Local || age < s.freshFor(t.Name):
				s.respond(w, t, args, e.Res, e.At, true, false, "")
				return
			case age < maxStale && !wait:
				// stale-while-revalidate: answer now, refresh behind it
				go s.refresh(t, key, args)
				s.respond(w, t, args, e.Res, e.At, true, true, "")
				return
			case age < maxStale:
				res, at, err := s.refresh(t, key, args)
				if err != nil {
					s.respond(w, t, args, e.Res, e.At, true, true, err.Error())
					return
				}
				s.respond(w, t, args, res, at, false, false, "")
				return
			}
		}
	}

	var res json.RawMessage
	var at time.Time
	if key != "" {
		res, at, err = s.refresh(t, key, args)
	} else {
		var out any
		out, err = s.reg.Call(s.app, t.Name, args)
		at = s.cfg.Now()
		if err == nil {
			res, err = json.Marshal(out)
		}
	}
	if err != nil {
		out := map[string]any{"error": err.Error(), "tool": t.Name}
		var d *drift.Error
		if errors.As(err, &d) {
			out["drift"] = true
			out["report_url"] = tools.DriftReportURL(err, at)
		}
		writeJSON(w, http.StatusBadGateway, out)
		return
	}
	if t.Kind == tools.Write && args.Bool("confirm", false) {
		// anything read before a binding change may be outdated now
		s.store.clear()
	}
	s.respond(w, t, args, res, at, false, false, "")
}

func (s *Server) freshFor(tool string) time.Duration {
	if s.cfg.CacheTTL > 0 {
		return s.cfg.CacheTTL
	}
	return freshness(tool)
}

// reusableDownload: downloads are kept and handed out again while the file
// exists, so opening the same Moodle file twice fetches it once.
func reusableDownload(t *tools.Tool) bool {
	return t.Kind == tools.Local && t.Name != "cis_login" && t.Name != "cis_logout"
}

func usable(e entry) bool {
	if e.Path == "" {
		return true
	}
	st, err := os.Stat(e.Path)
	return err == nil && !st.IsDir()
}

// refresh fetches a tool result (one flight per key) and stores it.
func (s *Server) refresh(t *tools.Tool, key string, args tools.Args) (json.RawMessage, time.Time, error) {
	res, at, err := s.store.fetch(key, s.cfg.Now, func() (any, error) {
		s.stats.fetch(t.Name)
		out, err := s.reg.Call(s.app, t.Name, args)
		if err != nil {
			s.stats.fail(t.Name)
		}
		return out, err
	})
	if err != nil {
		return nil, at, err
	}
	s.history.observe(t.Name, res)
	e := entry{Res: res, At: at}
	if t.Kind == tools.Local {
		e.Path = resultPath(res)
	}
	s.store.put(key, e)
	return res, at, nil
}

func resultPath(res json.RawMessage) string {
	var m struct {
		Path string `json:"path"`
	}
	json.Unmarshal(res, &m)
	return m.Path
}

func (s *Server) respond(w http.ResponseWriter, t *tools.Tool, args tools.Args, res json.RawMessage, at time.Time, hit, stale bool, refreshErr string) {
	if hit {
		s.stats.hit(t.Name)
	}
	res = s.withTranscript(t.Name, args, res, at)
	res = s.withPendingAttempts(t.Name, args, res)
	out := map[string]any{"tool": t.Name, "result": res, "cached": hit, "fetched_at": at.Format(time.RFC3339)}
	if stale {
		out["stale"] = true
	}
	if refreshErr != "" {
		out["refresh_error"] = refreshErr
	}
	if t.Kind == tools.Local {
		if u := s.downloadURL(resultPath(res)); u != "" {
			out["download_url"] = u
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) downloadURL(p string) string {
	rel, err := filepath.Rel(s.app.DownloadDir, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, x := range parts {
		parts[i] = url.PathEscape(x)
	}
	return "/files/" + strings.Join(parts, "/")
}

func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	// os.OpenInRoot refuses "..", absolute paths and symlinks leaving the dir.
	f, err := os.OpenInRoot(s.app.DownloadDir, r.PathValue("path"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	disp := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		if kind, ok := inlineTypes[strings.ToLower(filepath.Ext(st.Name()))]; ok {
			disp = "inline"
			if kind != "pdf" {
				// images/text open in a sandbox: no script runs on our origin
				w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; style-src 'unsafe-inline'")
			}
		}
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": st.Name()}))
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

// inlineTypes may open in the browser; everything else (HTML, SVG, Office
// files) is always a download. PDFs render in the browser's own viewer,
// which a sandbox CSP would block.
var inlineTypes = map[string]string{".pdf": "pdf", ".png": "img", ".jpg": "img", ".jpeg": "img", ".gif": "img", ".webp": "img", ".txt": "text"}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

// LoadOrCreateToken returns the access token stored in dir, creating a
// random one (mode 0600) on first start.
func LoadOrCreateToken(dir string) (tok string, created bool, err error) {
	return loadOrCreateSecret(dir, tokenFile)
}

func loadOrCreateSecret(dir, file string) (tok string, created bool, err error) {
	p := filepath.Join(dir, file)
	if b, err := os.ReadFile(p); err == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			return t, false, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", false, err
	}
	tok = base64.RawURLEncoding.EncodeToString(buf)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false, err
	}
	if err := os.WriteFile(p, []byte(tok+"\n"), 0o600); err != nil {
		return "", false, err
	}
	return tok, true, nil
}
