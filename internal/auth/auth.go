// Package auth performs the TYPO3 felogin flow of the CIS.
package auth

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Raindancer118/nak-api/internal/client"
	"github.com/Raindancer118/nak-api/internal/drift"
	"github.com/Raindancer118/nak-api/internal/forms"
	"github.com/Raindancer118/nak-api/internal/htmlx"
)

// ErrLoginRejected: the CIS answered the login but did not let us in (wrong credentials, or a changed
// login format as on 28.10.2026). Not an outage; the self-check reports it.
var ErrLoginRejected = errors.New("login failed — wrong credentials?")

// Login fetches the login form (hidden TYPO3 fields included), posts the
// credentials and stores the session cookie.
func Login(c *client.Client, username, password string) error {
	c.ClearSession()
	p, err := c.GetRaw("/")
	if err != nil {
		return fmt.Errorf("fetch login page: %w", err)
	}
	f := forms.FindByField(forms.Parse(htmlx.MustParse(p.Body), c.Base), "pass")
	if f == nil {
		return drift.New("/", "felogin form with a pass field", p.Body)
	}
	set := map[string]string{"user": CISUser(username), "pass": password}
	if f.Field("logintype") != nil {
		set["logintype"] = "login"
	}
	s, err := f.Submit("", set)
	if err != nil {
		return fmt.Errorf("build login: %w", err)
	}
	res, err := c.PostLogin(s.URL, s.Values())
	if err != nil {
		return fmt.Errorf("post login: %w", err)
	}
	if client.LooksLoggedOut(res) || !c.IsLoggedIn() {
		return ErrLoginRejected
	}
	return c.SaveSession()
}

// Domain is what the CIS login expects after the Matrikelnummer since 10/2026 ("20066@nordakademie.de").
const Domain = "nordakademie.de"

// CISUser completes a bare number to the address the CIS login wants; anything with an @ is kept as typed.
func CISUser(user string) string {
	user = strings.TrimSpace(user)
	if user == "" || strings.Contains(user, "@") {
		return user
	}
	return user + "@" + Domain
}

// ShortUser is the bare number for a NORDAKADEMIE address (what Moodle and the stored account use).
func ShortUser(user string) string {
	user = strings.TrimSpace(user)
	if local, domain, ok := strings.Cut(user, "@"); ok && strings.EqualFold(domain, Domain) {
		return local
	}
	return user
}

// SameUser: "20066" and "20066@nordakademie.de" are one account.
func SameUser(a, b string) bool {
	return strings.EqualFold(ShortUser(a), ShortUser(b))
}

// Logout ends the server session and removes the local cookie store.
func Logout(c *client.Client) error {
	_, err := c.GetRaw("/login/?logintype=logout")
	c.ClearSession()
	if err != nil {
		return fmt.Errorf("logout request: %w", err)
	}
	return nil
}

// EnvRelogin returns a Relogin hook using CIS_USER/CIS_PASS (nil if unset).
func EnvRelogin() func(*client.Client) error {
	u, p := os.Getenv("CIS_USER"), os.Getenv("CIS_PASS")
	if u == "" || p == "" {
		return nil
	}
	return func(c *client.Client) error { return Login(c, u, p) }
}
