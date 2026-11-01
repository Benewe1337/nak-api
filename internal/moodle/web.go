package moodle

import (
	"fmt"
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
