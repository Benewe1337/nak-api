// Package moodle talks to Moodle's mobile web service (login/token.php, the
// REST server and webservice/pluginfile.php) and shapes the results for LLM
// use. Port of the former Java moodle-mcp.
package moodle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Raindancer118/nak-api/internal/audit"
)

const DefaultURL = "https://moodle2.nordakademie.de"

// Error is a Moodle web service error (errorcode + message).
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string {
	if e.Code == "" {
		return e.Msg
	}
	return "[" + e.Code + "] " + e.Msg
}

var ErrReadOnly = errors.New("write blocked: Moodle client is in read-only mode (NAK_READONLY/MOODLE_READONLY)")

type Client struct {
	Base     string
	ReadOnly bool
	AuditDir string
	HTTP     *http.Client

	// CacheTTL > 0 shares identical read calls for that long; writes clear it.
	CacheTTL time.Duration
	Now      func() time.Time

	user, pass string
	mu         sync.Mutex
	token      string
	web        *http.Client // cookie session for HTML pages (WebGet)
	cache      map[string]cachedCall
}

type cachedCall struct {
	r  J
	at time.Time
}

func NewClient(baseURL, user, pass string) *Client {
	b := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if b == "" {
		b = DefaultURL
	}
	return &Client{Base: b, user: user, pass: pass, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

// Call runs a read-only web service function.
func (c *Client) Call(wsfunction string, params map[string]any) (J, error) {
	if c.CacheTTL <= 0 {
		return c.call(wsfunction, params)
	}
	flat := Flatten(params)
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(wsfunction)
	for _, k := range keys {
		b.WriteString("\n" + k + "=" + flat[k])
	}
	key := b.String()
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	c.mu.Lock()
	e, ok := c.cache[key]
	c.mu.Unlock()
	if ok && now().Sub(e.at) < c.CacheTTL {
		return e.r, nil
	}
	r, err := c.call(wsfunction, params)
	if err == nil {
		c.mu.Lock()
		if c.cache == nil {
			c.cache = map[string]cachedCall{}
		}
		c.cache[key] = cachedCall{r, now()}
		c.mu.Unlock()
	}
	return r, err
}

// Invalidate drops all shared reads.
func (c *Client) Invalidate() {
	c.mu.Lock()
	c.cache = nil
	c.mu.Unlock()
}

// CallWrite runs a state-changing function through the write guard.
func (c *Client) CallWrite(wsfunction string, params map[string]any) (J, error) {
	c.Invalidate()
	if c.ReadOnly {
		return J{}, ErrReadOnly
	}
	names := make([]string, 0, len(params))
	for k := range Flatten(params) {
		names = append(names, k)
	}
	sort.Strings(names)
	audit.Log(c.AuditDir, "moodle", "WS", wsfunction, names)
	return c.call(wsfunction, params)
}

func (c *Client) call(wsfunction string, params map[string]any) (J, error) {
	tok, err := c.Token()
	if err != nil {
		return J{}, err
	}
	r, err := c.callOnce(wsfunction, params, tok)
	if err != nil {
		return J{}, err
	}
	if isInvalidToken(r) {
		if tok, err = c.renewToken(); err != nil {
			return J{}, err
		}
		if r, err = c.callOnce(wsfunction, params, tok); err != nil {
			return J{}, err
		}
	}
	if r.IsObj() && r.Has("exception") {
		return J{}, &Error{Code: r.Get("errorcode").Str(""), Msg: r.Get("message").Str("Moodle error")}
	}
	return r, nil
}

func (c *Client) callOnce(wsfunction string, params map[string]any, tok string) (J, error) {
	form := url.Values{}
	form.Set("wstoken", tok)
	form.Set("moodlewsrestformat", "json")
	form.Set("wsfunction", wsfunction)
	for k, v := range Flatten(params) {
		form.Set(k, v)
	}
	return c.postJSON("/webservice/rest/server.php", form)
}

func isInvalidToken(r J) bool { return r.IsObj() && r.Get("errorcode").Str("") == "invalidtoken" }

// Token returns the cached web service token, logging in on first use.
func (c *Client) Token() (string, error) {
	c.mu.Lock()
	t := c.token
	c.mu.Unlock()
	if t != "" {
		return t, nil
	}
	return c.renewToken()
}

func (c *Client) renewToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.user == "" || c.pass == "" {
		return "", &Error{Code: "no_credentials", Msg: "MOODLE_USER/MOODLE_PASS are not set"}
	}
	form := url.Values{"username": {c.user}, "password": {c.pass}, "service": {"moodle_mobile_app"}}
	r, err := c.postJSON("/login/token.php", form)
	if err != nil {
		return "", err
	}
	t := r.Get("token").Str("")
	if t == "" {
		return "", &Error{Code: r.Get("errorcode").Str("login_failed"), Msg: "Moodle login failed: " + r.Get("error").Str("no token returned")}
	}
	c.token = t
	return t, nil
}

func (c *Client) postJSON(path string, form url.Values) (J, error) {
	resp, err := c.HTTP.PostForm(c.Base+path, form)
	if err != nil {
		// never leak the form (password/token) in errors
		return J{}, &Error{Msg: "request to Moodle failed: " + scrubURLError(err)}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return J{}, &Error{Msg: "reading Moodle response failed: " + err.Error()}
	}
	if resp.StatusCode != http.StatusOK {
		return J{}, &Error{Code: "http_" + strconv.Itoa(resp.StatusCode), Msg: fmt.Sprintf("Moodle answered HTTP %d for %s", resp.StatusCode, path)}
	}
	j, err := parseJ(b)
	if err != nil {
		return J{}, &Error{Msg: "Moodle returned no valid JSON for " + path}
	}
	return j, nil
}

func scrubURLError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// Flatten converts params into Moodle's REST form: lists become key[0], maps
// key[name], booleans 1/0.
func Flatten(params map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range params {
		flatten(k, v, out)
	}
	return out
}

func flatten(key string, v any, out map[string]string) {
	switch t := v.(type) {
	case nil:
	case map[string]any:
		for k, x := range t {
			flatten(key+"["+k+"]", x, out)
		}
	case []any:
		for i, x := range t {
			flatten(key+"["+strconv.Itoa(i)+"]", x, out)
		}
	case []int:
		for i, x := range t {
			out[key+"["+strconv.Itoa(i)+"]"] = strconv.Itoa(x)
		}
	case []int64:
		for i, x := range t {
			out[key+"["+strconv.Itoa(i)+"]"] = strconv.FormatInt(x, 10)
		}
	case []string:
		for i, x := range t {
			out[key+"["+strconv.Itoa(i)+"]"] = x
		}
	case []map[string]any:
		for i, x := range t {
			flatten(key+"["+strconv.Itoa(i)+"]", x, out)
		}
	case bool:
		if t {
			out[key] = "1"
		} else {
			out[key] = "0"
		}
	case float64:
		if t == float64(int64(t)) {
			out[key] = strconv.FormatInt(int64(t), 10)
		} else {
			out[key] = strconv.FormatFloat(t, 'f', -1, 64)
		}
	default:
		out[key] = fmt.Sprint(t)
	}
}

// ── files ───────────────────────────────────────────────────────────────────

type Fetched struct {
	Body        []byte
	ContentType string
	FileName    string
}

// Fetch downloads a pluginfile URL of this Moodle using the token.
func (c *Client) Fetch(fileURL string) (*Fetched, error) {
	u, err := c.webserviceFileURL(fileURL)
	if err != nil {
		return nil, err
	}
	tok, err := c.Token()
	if err != nil {
		return nil, err
	}
	f, err := c.fetchOnce(u, tok)
	if err == errTokenRejected {
		if tok, err = c.renewToken(); err != nil {
			return nil, err
		}
		f, err = c.fetchOnce(u, tok)
	}
	if err == errTokenRejected {
		return nil, &Error{Code: "invalidtoken", Msg: "file download rejected the token"}
	}
	return f, err
}

var errTokenRejected = errors.New("token rejected")

func (c *Client) fetchOnce(u *url.URL, tok string) (*Fetched, error) {
	q := u.Query()
	q.Set("token", tok)
	withTok := *u
	withTok.RawQuery = q.Encode()
	resp, err := c.HTTP.Get(withTok.String())
	if err != nil {
		return nil, &Error{Msg: "file download failed: " + scrubURLError(err)}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, &Error{Msg: "file download failed: " + err.Error()}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &Error{Code: "http_" + strconv.Itoa(resp.StatusCode), Msg: fmt.Sprintf("file download failed with HTTP %d", resp.StatusCode)}
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	if strings.HasPrefix(ct, "application/json") {
		// pluginfile reports errors as JSON with status 200
		if j, err := parseJ(b); err == nil && j.IsObj() && j.Has("errorcode") {
			if isInvalidToken(j) {
				return nil, errTokenRejected
			}
			return nil, &Error{Code: j.Get("errorcode").Str(""), Msg: j.Get("error").Str("file error")}
		}
	}
	return &Fetched{Body: b, ContentType: ct, FileName: FileName(u)}, nil
}

func (c *Client) webserviceFileURL(fileURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(fileURL))
	if err != nil || u.Host == "" {
		return nil, &Error{Code: "bad_url", Msg: "not a valid URL: " + fileURL}
	}
	b, _ := url.Parse(c.Base)
	if !strings.EqualFold(u.Scheme, b.Scheme) || !strings.EqualFold(u.Host, b.Host) {
		return nil, &Error{Code: "bad_url", Msg: "only files from " + c.Base + " can be fetched"}
	}
	path := strings.TrimPrefix(u.EscapedPath(), b.EscapedPath())
	switch {
	case strings.HasPrefix(path, "/pluginfile.php/"):
		path = "/webservice" + path
	case strings.HasPrefix(path, "/webservice/pluginfile.php/"):
	default:
		return nil, &Error{Code: "bad_url", Msg: "only pluginfile.php URLs are supported (got " + path + ")"}
	}
	out, err := url.Parse(b.Scheme + "://" + b.Host + b.EscapedPath() + path)
	if err != nil {
		return nil, &Error{Code: "bad_url", Msg: err.Error()}
	}
	out.RawQuery = u.RawQuery
	return out, nil
}

// FileName derives a safe file name from the last path segment.
func FileName(u *url.URL) string {
	raw := u.EscapedPath()
	seg := raw[strings.LastIndex(raw, "/")+1:]
	if s, err := url.PathUnescape(seg); err == nil {
		seg = s
	}
	if i := strings.LastIndexAny(seg, `/\`); i >= 0 {
		seg = seg[i+1:]
	}
	seg = strings.Map(func(r rune) rune {
		if r < 0x20 {
			return -1
		}
		return r
	}, strings.TrimSpace(seg))
	if seg == "" || seg == "." || seg == ".." {
		return "download"
	}
	return seg
}

// Download stores the file in dir and never overwrites ("name (n).ext").
func (c *Client) Download(fileURL, dir string) (string, int, error) {
	f, err := c.Fetch(fileURL)
	if err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, &Error{Msg: "could not create " + dir + ": " + err.Error()}
	}
	target := FreeName(dir, f.FileName)
	if err := os.WriteFile(target, f.Body, 0o644); err != nil {
		return "", 0, &Error{Msg: "could not write file: " + err.Error()}
	}
	return target, len(f.Body), nil
}

func FreeName(dir, name string) string {
	p := filepath.Join(dir, name)
	if filepath.Dir(p) != filepath.Clean(dir) {
		p = filepath.Join(dir, "download")
	}
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	base := filepath.Base(p)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" {
		stem, ext = base, ""
	}
	for i := 1; ; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}

// UploadDraft uploads files into one new draft area and returns its itemid.
func (c *Client) UploadDraft(files []string) (int64, error) {
	if c.ReadOnly {
		return 0, ErrReadOnly
	}
	audit.Log(c.AuditDir, "moodle", "POST", "/webservice/upload.php", baseNames(files))
	tok, err := c.Token()
	if err != nil {
		return 0, err
	}
	r, err := c.uploadOnce(files, tok)
	if err == nil && isInvalidToken(r) {
		if tok, err = c.renewToken(); err != nil {
			return 0, err
		}
		r, err = c.uploadOnce(files, tok)
	}
	if err != nil {
		return 0, err
	}
	if r.IsObj() && r.Has("errorcode") {
		return 0, &Error{Code: r.Get("errorcode").Str(""), Msg: "upload failed: " + r.Get("error").Str("")}
	}
	if !r.IsArr() || r.Len() == 0 {
		return 0, &Error{Code: "upload_failed", Msg: "upload returned no files"}
	}
	return r.Idx(0).Get("itemid").Int(0), nil
}

func baseNames(files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = filepath.Base(f)
	}
	return out
}

func (c *Client) uploadOnce(files []string, tok string) (J, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, kv := range [][2]string{{"token", tok}, {"filearea", "draft"}, {"itemid", "0"}} {
		_ = mw.WriteField(kv[0], kv[1])
	}
	for i, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return J{}, &Error{Msg: "could not read file for upload: " + err.Error()}
		}
		w, err := mw.CreateFormFile("file_"+strconv.Itoa(i+1), strings.ReplaceAll(filepath.Base(f), `"`, "'"))
		if err != nil {
			return J{}, err
		}
		w.Write(data)
	}
	mw.Close()
	resp, err := c.HTTP.Post(c.Base+"/webservice/upload.php", mw.FormDataContentType(), &buf)
	if err != nil {
		return J{}, &Error{Msg: "upload failed: " + scrubURLError(err)}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return J{}, &Error{Code: "http_" + strconv.Itoa(resp.StatusCode), Msg: fmt.Sprintf("upload failed with HTTP %d", resp.StatusCode)}
	}
	j, err := parseJ(b)
	if err != nil {
		return J{}, &Error{Msg: "upload returned no valid JSON"}
	}
	return j, nil
}
