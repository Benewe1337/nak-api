// Package client is the HTTP layer for the CIS. Reads and writes go through
// separate methods: every request that changes state in the CIS (exam
// registration, profile edits, …) must use a Write* method, which honours the
// ReadOnly switch and is audit-logged.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Raindancer118/nak-api/internal/audit"
)

const (
	DefaultBaseURL = "https://cis.nordakademie.de"
	UserAgent      = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	SessionCookie  = "fe_typo_user_cae070b"
	maxBody        = 64 << 20
)

// BaseURL is kept for callers that only need the production host.
const BaseURL = DefaultBaseURL

var (
	ErrReadOnly    = errors.New("write blocked: client is in read-only mode (CIS_READONLY)")
	ErrNotLoggedIn = errors.New("not logged in to the CIS (session missing or expired) — call cis_login")
)

type Client struct {
	HTTP *http.Client
	// Base is the scheme+host all paths are resolved against.
	Base string
	// ReadOnly makes every Write* call fail before any network I/O.
	ReadOnly bool
	// Relogin, if set, is called once when a read hits an expired session.
	Relogin func(*Client) error
	// CacheTTL > 0 shares identical reads for that long, so tools that need
	// the same page (dashboard, deadlines, module view) fetch it once. Any
	// write clears it.
	CacheTTL time.Duration
	Now      func() time.Time

	jar        *cookiejar.Jar
	sessionDir string

	cacheMu sync.Mutex
	cache   map[string]cachedPage
	// loginMu + loginGen let parallel reads that all hit an expired session
	// share one relogin.
	loginMu  sync.Mutex
	loginGen int
}

type cachedPage struct {
	p  *Page
	at time.Time
}

type Options struct {
	BaseURL    string
	SessionDir string
}

// Page is a fetched response.
type Page struct {
	URL         string
	Status      int
	Body        string
	ContentType string
	Header      http.Header
}

// FilePart is one file of a multipart upload.
type FilePart struct {
	Field       string
	Filename    string
	ContentType string
	Data        []byte
}

// New creates a production client with the session from ~/.config/cis-api.
// CIS_READONLY=1 forces read-only mode; CIS_BASE_URL overrides the host (tests).
func New() (*Client, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	c, err := NewWith(Options{BaseURL: os.Getenv("CIS_BASE_URL"), SessionDir: filepath.Join(home, ".config", "cis-api")})
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(os.Getenv("CIS_READONLY")) {
	case "1", "true", "yes":
		c.ReadOnly = true
	}
	return c, nil
}

func NewWith(o Options) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("cookie jar: %w", err)
	}
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	c := &Client{Base: strings.TrimRight(o.BaseURL, "/"), jar: jar, sessionDir: o.SessionDir}
	c.HTTP = &http.Client{
		Jar:     jar,
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			req.Header.Set("User-Agent", UserAgent)
			return nil
		},
	}
	_ = c.loadSession() // no session yet is fine
	return c, nil
}

// Abs resolves a path (or validates an absolute URL) against the CIS host.
// Absolute URLs pointing elsewhere are rejected so tools can never be used to
// fetch or post to arbitrary hosts.
func (c *Client) Abs(pathOrURL string) (string, error) {
	s := strings.TrimSpace(strings.ReplaceAll(pathOrURL, "&amp;", "&"))
	if s == "" {
		return "", errors.New("empty URL")
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", err
		}
		b, _ := url.Parse(c.Base)
		if u.Host != b.Host {
			return "", fmt.Errorf("refusing URL outside %s: %s", b.Host, u.Host)
		}
		return s, nil
	}
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return c.Base + s, nil
}

// Page GETs a page (read-only). An expired session triggers one Relogin.
func (c *Client) Page(pathOrURL string) (*Page, error) {
	return c.cachedRead("GET "+pathOrURL, func() (*Page, error) {
		return c.read(func() (*http.Request, error) { return c.newReq(http.MethodGet, pathOrURL, nil, "") })
	})
}

// PostRead submits a form that only displays data (e.g. a detail view or a
// filter select). Never use it for anything that changes state.
func (c *Client) PostRead(pathOrURL string, data url.Values) (*Page, error) {
	enc := data.Encode()
	return c.cachedRead("POST "+pathOrURL+"\n"+enc, func() (*Page, error) {
		return c.read(func() (*http.Request, error) {
			return c.newReq(http.MethodPost, pathOrURL, strings.NewReader(enc), "application/x-www-form-urlencoded")
		})
	})
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) cachedRead(key string, fetch func() (*Page, error)) (*Page, error) {
	if c.CacheTTL <= 0 {
		return fetch()
	}
	c.cacheMu.Lock()
	e, ok := c.cache[key]
	c.cacheMu.Unlock()
	if ok && c.now().Sub(e.at) < c.CacheTTL {
		return e.p, nil
	}
	p, err := fetch()
	if err == nil && p.Status < 400 {
		c.cacheMu.Lock()
		if c.cache == nil {
			c.cache = map[string]cachedPage{}
		}
		c.cache[key] = cachedPage{p, c.now()}
		c.cacheMu.Unlock()
	}
	return p, err
}

// Invalidate drops all shared reads (after writes and session changes).
func (c *Client) Invalidate() {
	c.cacheMu.Lock()
	c.cache = nil
	c.cacheMu.Unlock()
}

// Download fetches a file (read-only) and returns its bytes, content type and
// the server-suggested filename (may be empty).
func (c *Client) Download(pathOrURL string) ([]byte, string, string, error) {
	p, err := c.read(func() (*http.Request, error) { return c.newReq(http.MethodGet, pathOrURL, nil, "") })
	if err != nil {
		return nil, "", "", err
	}
	if p.Status >= 400 {
		return nil, p.ContentType, "", fmt.Errorf("download: HTTP %d", p.Status)
	}
	return []byte(p.Body), p.ContentType, filenameFrom(p.Header.Get("Content-Disposition")), nil
}

func (c *Client) read(build func() (*http.Request, error)) (*Page, error) {
	for attempt := 0; ; attempt++ {
		c.loginMu.Lock()
		gen := c.loginGen
		c.loginMu.Unlock()
		req, err := build()
		if err != nil {
			return nil, err
		}
		p, err := c.do(req)
		if err != nil {
			return nil, err
		}
		if !LooksLoggedOut(p) {
			return p, nil
		}
		if attempt > 0 || c.Relogin == nil {
			return nil, ErrNotLoggedIn
		}
		if err := c.relogin(gen); err != nil {
			return nil, fmt.Errorf("%w (re-login failed: %w)", ErrNotLoggedIn, err)
		}
	}
}

// relogin logs in unless another request already did since gen was read.
func (c *Client) relogin(gen int) error {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	if c.loginGen != gen {
		return nil
	}
	if err := c.Relogin(c); err != nil {
		return err
	}
	c.loginGen++
	c.Invalidate()
	return nil
}

// WriteGet follows a state-changing link (TYPO3 often uses GET for actions
// such as exam registration).
func (c *Client) WriteGet(pathOrURL string) (*Page, error) {
	if err := c.guard(http.MethodGet, pathOrURL, nil); err != nil {
		return nil, err
	}
	req, err := c.newReq(http.MethodGet, pathOrURL, nil, "")
	if err != nil {
		return nil, err
	}
	return c.write(req)
}

func (c *Client) WritePost(pathOrURL string, data url.Values) (*Page, error) {
	if err := c.guard(http.MethodPost, pathOrURL, keys(data)); err != nil {
		return nil, err
	}
	req, err := c.newReq(http.MethodPost, pathOrURL, strings.NewReader(data.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return nil, err
	}
	return c.write(req)
}

// WriteMultipart posts fields in the given order plus files.
func (c *Client) WriteMultipart(pathOrURL string, fields []KV, files []FilePart) (*Page, error) {
	names := make([]string, 0, len(fields)+len(files))
	for _, f := range fields {
		names = append(names, f.Name)
	}
	for _, f := range files {
		names = append(names, f.Field+"=@"+f.Filename)
	}
	if err := c.guard(http.MethodPost, pathOrURL, names); err != nil {
		return nil, err
	}
	body, ct, err := EncodeMultipart(fields, files)
	if err != nil {
		return nil, err
	}
	req, err := c.newReq(http.MethodPost, pathOrURL, bytes.NewReader(body), ct)
	if err != nil {
		return nil, err
	}
	return c.write(req)
}

// KV is an ordered form field; forms may repeat names (checkbox + hidden).
type KV struct {
	Name, Value string
}

func EncodeMultipart(fields []KV, files []FilePart) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range fields {
		if err := mw.WriteField(f.Name, f.Value); err != nil {
			return nil, "", err
		}
	}
	for _, f := range files {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeQuotes(f.Field), escapeQuotes(f.Filename)))
		ct := f.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		h.Set("Content-Type", ct)
		w, err := mw.CreatePart(h)
		if err != nil {
			return nil, "", err
		}
		if _, err := w.Write(f.Data); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

func escapeQuotes(s string) string { return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) }

func (c *Client) guard(method, target string, fieldNames []string) error {
	c.Invalidate()
	if c.ReadOnly {
		return ErrReadOnly
	}
	c.audit(method, target, fieldNames)
	return nil
}

func (c *Client) write(req *http.Request) (*Page, error) {
	p, err := c.do(req)
	if err != nil {
		return nil, err
	}
	if LooksLoggedOut(p) {
		// The server rejected the request before processing it; the caller must
		// re-run the whole flow (fresh form tokens) after logging in.
		return nil, ErrNotLoggedIn
	}
	return p, nil
}

// audit logs method, URL (without cHash) and field names — never values.
func (c *Client) audit(method, target string, fieldNames []string) {
	u := target
	if pu, err := url.Parse(target); err == nil {
		q := pu.Query()
		q.Del("cHash")
		pu.RawQuery, _ = url.QueryUnescape(q.Encode())
		u = pu.String()
	}
	audit.Log(c.sessionDir, "cis", method, u, fieldNames)
}

func keys(v url.Values) []string {
	out := make([]string, 0, len(v))
	for k := range v {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (c *Client) newReq(method, pathOrURL string, body io.Reader, contentType string) (*http.Request, error) {
	u, err := c.Abs(pathOrURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Origin", c.Base)
		req.Header.Set("Referer", u)
	}
	return req, nil
}

func (c *Client) do(req *http.Request) (*Page, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	return &Page{URL: resp.Request.URL.String(), Status: resp.StatusCode, Body: string(b),
		ContentType: resp.Header.Get("Content-Type"), Header: resp.Header}, nil
}

// LooksLoggedOut reports whether the CIS answered with its login page
// (HTTP 403 + felogin form) instead of the requested content.
func LooksLoggedOut(p *Page) bool {
	for _, bin := range []string{"pdf", "octet-stream", "calendar", "image/", "zip"} {
		if strings.Contains(p.ContentType, bin) {
			return false
		}
	}
	return strings.Contains(p.Body, `name="pass"`) && (p.Status == http.StatusForbidden || !strings.Contains(p.Body, "logintype=logout"))
}

// GetRaw fetches without logged-out detection or relogin — only for reading
// the login form itself.
func (c *Client) GetRaw(pathOrURL string) (*Page, error) {
	req, err := c.newReq(http.MethodGet, pathOrURL, nil, "")
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

// PostLogin is only for the felogin form itself.
func (c *Client) PostLogin(actionURL string, data url.Values) (*Page, error) {
	req, err := c.newReq(http.MethodPost, actionURL, strings.NewReader(data.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

func (c *Client) IsLoggedIn() bool {
	u, _ := url.Parse(c.Base)
	for _, ck := range c.jar.Cookies(u) {
		if ck.Name == SessionCookie && ck.Value != "" {
			return true
		}
	}
	return false
}

type savedCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (c *Client) SaveSession() error {
	if c.sessionDir == "" {
		return nil
	}
	if err := os.MkdirAll(c.sessionDir, 0o700); err != nil {
		return err
	}
	u, _ := url.Parse(c.Base)
	var saved []savedCookie
	for _, ck := range c.jar.Cookies(u) {
		saved = append(saved, savedCookie{Name: ck.Name, Value: ck.Value})
	}
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	return os.WriteFile(c.sessionFile(), data, 0o600)
}

func (c *Client) sessionFile() string {
	name := "session.json"
	if c.Base != DefaultBaseURL {
		// Keeps test servers from overwriting the real session.
		name = "session-" + strings.NewReplacer(":", "_", "/", "_").Replace(strings.TrimPrefix(strings.TrimPrefix(c.Base, "https://"), "http://")) + ".json"
	}
	return filepath.Join(c.sessionDir, name)
}

func (c *Client) loadSession() error {
	if c.sessionDir == "" {
		return nil
	}
	data, err := os.ReadFile(c.sessionFile())
	if err != nil {
		return err
	}
	var saved []savedCookie
	if err := json.Unmarshal(data, &saved); err != nil {
		return err
	}
	u, _ := url.Parse(c.Base)
	var cookies []*http.Cookie
	for _, s := range saved {
		cookies = append(cookies, &http.Cookie{Name: s.Name, Value: s.Value, Path: "/"})
	}
	c.jar.SetCookies(u, cookies)
	return nil
}

// ClearSession drops cookies in memory and on disk.
func (c *Client) ClearSession() {
	c.Invalidate()
	if c.sessionDir != "" {
		os.Remove(c.sessionFile())
	}
	jar, _ := cookiejar.New(nil)
	c.jar = jar
	c.HTTP.Jar = jar
}

func filenameFrom(cd string) string {
	for _, part := range strings.Split(cd, ";") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "filename*=UTF-8''"); ok {
			if s, err := url.PathUnescape(v); err == nil {
				return s
			}
		}
		if v, ok := strings.CutPrefix(part, "filename="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}
