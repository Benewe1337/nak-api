// Package app wires the CIS and Moodle clients from the environment.
package app

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Raindancer118/nak-api/internal/auth"
	"github.com/Raindancer118/nak-api/internal/client"
	"github.com/Raindancer118/nak-api/internal/eduvault"
	"github.com/Raindancer118/nak-api/internal/mensa"
	"github.com/Raindancer118/nak-api/internal/moodle"
)

// App is the shared context of all tools. Clients are created lazily so the
// MCP server starts instantly and works offline until a tool needs a system.
type App struct {
	Zone        *time.Location
	Now         func() time.Time
	DownloadDir string
	ConfigDir   string
	ReadOnly    bool
	// ClientCache shares identical CIS/Moodle reads for this long.
	ClientCache time.Duration

	cisUser, cisPass       string
	accountSource          string
	moodleUser, moodlePass string
	moodleURL              string
	cisBase                string

	mu       sync.Mutex
	cis      *client.Client
	moodle   *moodle.Service
	eduvault *eduvault.Client
	mensa    *mensa.Client
	settings *Settings
}

// FromEnv reads CIS_USER/CIS_PASS, MOODLE_USER/MOODLE_PASS (falling back to
// the CIS account — both use the NAK login), NAK_READONLY, NAK_DOWNLOAD_DIR,
// MOODLE_URL, CIS_BASE_URL and NAK_TZ. NAK_DATA_DIR moves the session, audit
// log and (unless NAK_DOWNLOAD_DIR is set) the downloads into one directory.
func FromEnv() *App {
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".config", "cis-api")
	downloadDir := filepath.Join(home, "Downloads", "nak")
	if d := strings.TrimSpace(os.Getenv("NAK_DATA_DIR")); d != "" {
		configDir, downloadDir = d, filepath.Join(d, "downloads")
	}
	zone, err := time.LoadLocation(envOr("NAK_TZ", "Europe/Berlin"))
	if err != nil {
		zone = time.UTC
	}
	a := &App{
		Zone:        zone,
		Now:         time.Now,
		DownloadDir: envOr("NAK_DOWNLOAD_DIR", downloadDir),
		ConfigDir:   configDir,
		ReadOnly:    truthy(os.Getenv("NAK_READONLY")) || truthy(os.Getenv("CIS_READONLY")),
		cisUser:     os.Getenv("CIS_USER"),
		cisPass:     os.Getenv("CIS_PASS"),
		moodleUser:  envOr("MOODLE_USER", os.Getenv("CIS_USER")),
		moodlePass:  envOr("MOODLE_PASS", os.Getenv("CIS_PASS")),
		moodleURL:   envOr("MOODLE_URL", moodle.DefaultURL),
		cisBase:     os.Getenv("CIS_BASE_URL"),
		ClientCache: 90 * time.Second,
	}
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv("NAK_CLIENT_CACHE_TTL"))); err == nil {
		a.ClientCache = d
	}
	if a.cisUser != "" && a.cisPass != "" {
		a.accountSource = "env"
	} else if acc, ok := loadAccount(a.ConfigDir); ok {
		a.useAccount(acc.User, acc.Pass)
		a.accountSource = "file"
	}
	return a
}

type account struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

func loadAccount(dir string) (account, bool) {
	var acc account
	b, err := os.ReadFile(filepath.Join(dir, "account.json"))
	if err != nil || json.Unmarshal(b, &acc) != nil || acc.User == "" || acc.Pass == "" {
		return account{}, false
	}
	return acc, true
}

func (a *App) useAccount(user, pass string) {
	a.cisUser, a.cisPass = user, pass
	if os.Getenv("MOODLE_USER") == "" || os.Getenv("MOODLE_PASS") == "" {
		a.moodleUser, a.moodlePass = user, pass
	}
}

// AccountUser is the NAK account this instance belongs to ("" before the
// first web login when no CIS_USER is set).
func (a *App) AccountUser() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cisUser
}

// AccountSource is "env", "file" (saved by the web login) or "".
func (a *App) AccountSource() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.accountSource
}

// PasswordMatches compares in constant time (login without asking the CIS).
func (a *App) PasswordMatches(user, pass string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cisUser == "" || !auth.SameUser(user, a.cisUser) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(pass), []byte(a.cisPass)) == 1
}

// SaveAccount stores the NAK login (0600; the CIS and Moodle need the
// password itself for their own logins) and resets both clients.
func (a *App) SaveAccount(user, pass string) error {
	// Stored short ("20066"): Moodle reuses it; the CIS login completes it (auth.CISUser).
	user = auth.ShortUser(user)
	b, err := json.Marshal(account{user, pass})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.ConfigDir, 0o700); err != nil {
		return err
	}
	f := filepath.Join(a.ConfigDir, "account.json")
	if err := os.WriteFile(f+".tmp", b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(f+".tmp", f); err != nil {
		return err
	}
	a.mu.Lock()
	a.useAccount(user, pass)
	if a.accountSource == "" {
		a.accountSource = "file"
	}
	a.cis, a.moodle, a.mensa = nil, nil, nil
	a.mu.Unlock()
	return nil
}

// CheckLogin tries user/pass against the CIS without touching the stored
// session (a wrong password must not log the instance out).
func (a *App) CheckLogin(user, pass string) error {
	c, err := client.NewWith(client.Options{BaseURL: a.cisBase})
	if err != nil {
		return err
	}
	return auth.Login(c, strings.TrimSpace(user), pass)
}

func envOr(k, d string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return d
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "ja":
		return true
	}
	return false
}

// UseCIS / UseMoodle inject clients (tests, custom hosts).
func (a *App) UseCIS(c *client.Client) {
	a.mu.Lock()
	a.cis = c
	a.mu.Unlock()
}

func (a *App) UseMoodle(s *moodle.Service) {
	a.mu.Lock()
	a.moodle = s
	a.mu.Unlock()
}

// CIS returns the logged-in CIS client, logging in from the environment when
// no valid session cookie exists.
func (a *App) CIS() (*client.Client, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cis == nil {
		c, err := client.NewWith(client.Options{BaseURL: a.cisBase, SessionDir: a.ConfigDir})
		if err != nil {
			return nil, err
		}
		c.ReadOnly = a.ReadOnly
		c.CacheTTL, c.Now = a.ClientCache, a.Now
		if a.cisUser != "" && a.cisPass != "" {
			u, p := a.cisUser, a.cisPass
			c.Relogin = func(c *client.Client) error { return auth.Login(c, u, p) }
		}
		a.cis = c
	}
	if !a.cis.IsLoggedIn() {
		if a.cis.Relogin == nil {
			return nil, client.ErrNotLoggedIn
		}
		if err := a.cis.Relogin(a.cis); err != nil {
			return nil, fmt.Errorf("CIS login: %w", err)
		}
	}
	return a.cis, nil
}

// CISClient returns the client without forcing a login (for cis_login/status).
func (a *App) CISClient() (*client.Client, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cis == nil {
		c, err := client.NewWith(client.Options{BaseURL: a.cisBase, SessionDir: a.ConfigDir})
		if err != nil {
			return nil, err
		}
		c.ReadOnly = a.ReadOnly
		c.CacheTTL, c.Now = a.ClientCache, a.Now
		a.cis = c
	}
	return a.cis, nil
}

// Mensa: the canteen with the NAK login (balance and bookings need it, the
// menu does not).
func (a *App) Mensa() *mensa.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mensa == nil {
		a.mensa = mensa.New(envOr("MENSA_URL", mensa.DefaultURL), a.cisUser, a.cisPass)
		a.mensa.Zone = a.Zone
	}
	return a.mensa
}

// UseMensa injects a client (tests).
func (a *App) UseMensa(c *mensa.Client) {
	a.mu.Lock()
	a.mensa = c
	a.mu.Unlock()
}

func (a *App) Moodle() (*moodle.Service, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.moodle == nil {
		if a.moodleUser == "" || a.moodlePass == "" {
			return nil, fmt.Errorf("Moodle credentials missing: set MOODLE_USER/MOODLE_PASS (or CIS_USER/CIS_PASS)")
		}
		mc := moodle.NewClient(a.moodleURL, a.moodleUser, a.moodlePass)
		mc.ReadOnly = a.ReadOnly
		mc.AuditDir = a.ConfigDir
		mc.CacheTTL, mc.Now = a.ClientCache, a.Now
		s := moodle.NewService(mc, a.Zone)
		s.Now = a.Now
		a.moodle = s
	}
	return a.moodle, nil
}

// ExpandPath resolves "~/" and relative paths (relative to DownloadDir).
func (a *App) ExpandPath(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	if p != "" && !filepath.IsAbs(p) {
		return filepath.Join(a.DownloadDir, p)
	}
	return p
}
