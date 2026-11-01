package moodle

import (
	"fmt"
	"github.com/Raindancer118/nak-api/internal/audit"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
)

var loginToken = regexp.MustCompile(`name="logintoken"\s+value="([^"]*)"`)

const loginPath = "/login/index.php"

// WebGet fetches a logged-in HTML page (GET only; the mobile token cannot see
// pages like mod/scheduler). The session is created lazily and renewed once
// when Moodle bounces to the login form.
func (c *Client) WebGet(path string) (string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		hc, err := c.webClient()
		if err != nil {
			return "", err
		}
		resp, err := hc.Get(c.Base + path)
		if err != nil {
			return "", &Error{Msg: "request to Moodle failed: " + scrubURLError(err)}
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil {
			return "", &Error{Msg: "reading Moodle response failed: " + err.Error()}
		}
		if resp.Request.URL.Path == loginPath {
			c.dropWebSession()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return "", &Error{Code: fmt.Sprintf("http_%d", resp.StatusCode), Msg: fmt.Sprintf("Moodle answered HTTP %d for %s", resp.StatusCode, path)}
		}
		return string(b), nil
	}
	return "", &Error{Code: "login_failed", Msg: "Moodle web session could not be established"}
}

func (c *Client) dropWebSession() {
	c.mu.Lock()
	c.web = nil
	c.mu.Unlock()
}

func (c *Client) webClient() (*http.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.web != nil {
		return c.web, nil
	}
	if c.user == "" || c.pass == "" {
		return nil, &Error{Code: "no_credentials", Msg: "MOODLE_USER/MOODLE_PASS are not set"}
	}
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Timeout: c.HTTP.Timeout, Jar: jar}
	resp, err := hc.Get(c.Base + loginPath)
	if err != nil {
		return nil, &Error{Msg: "request to Moodle failed: " + scrubURLError(err)}
	}
	page, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	form := url.Values{"username": {c.user}, "password": {c.pass}, "anchor": {""}}
	if m := loginToken.FindSubmatch(page); m != nil {
		form.Set("logintoken", string(m[1]))
	}
	resp, err = hc.PostForm(c.Base+loginPath, form)
	if err != nil {
		return nil, &Error{Msg: "request to Moodle failed: " + scrubURLError(err)}
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	if strings.HasSuffix(resp.Request.URL.Path, loginPath) {
		return nil, &Error{Code: "login_failed", Msg: "Moodle web login failed: credentials rejected"}
	}
	c.web = hc
	return hc, nil
}

// ErrSessionExpired: the POST ended on the login form.
var ErrSessionExpired = &Error{Code: "session_expired", Msg: "Moodle web session expired"}

// WebPost submits a form of the logged-in web UI (write path: guarded by
// ReadOnly, audited with field names only) and returns the page the redirect
// chain ends on.
func (c *Client) WebPost(action string, fields []FormField) (string, error) {
	c.Invalidate()
	if c.ReadOnly {
		return "", ErrReadOnly
	}
	names := make([]string, len(fields))
	form := url.Values{}
	for i, f := range fields {
		names[i] = f.Name
		form.Add(f.Name, f.Value)
	}
	u, err := url.Parse(action)
	if err != nil || !c.sameHost(u) {
		return "", &Error{Code: "bad_target", Msg: "refusing to post to " + action}
	}
	audit.Log(c.AuditDir, "moodle", "WEB", u.Path, names)
	hc, err := c.webClient()
	if err != nil {
		return "", err
	}
	resp, err := hc.PostForm(u.String(), form)
	if err != nil {
		return "", &Error{Msg: "request to Moodle failed: " + scrubURLError(err)}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return "", &Error{Msg: "reading Moodle response failed: " + err.Error()}
	}
	if resp.Request.URL.Path == loginPath {
		c.dropWebSession()
		return "", ErrSessionExpired
	}
	return string(b), nil
}

func (c *Client) sameHost(u *url.URL) bool {
	b, err := url.Parse(c.Base)
	return err == nil && u.Host == b.Host && u.Scheme == b.Scheme
}
