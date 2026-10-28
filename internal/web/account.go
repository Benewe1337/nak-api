package web

import (
	"github.com/Raindancer118/nak-api/internal/auth"

	"net/http"
	"os"
	"strings"
)

// tryAccountLogin signs in with the NAK account. The first successful login
// makes that account the owner of the instance; later logins compare locally
// and only ask the CIS when the password differs (changed at the NAK).
func (s *Server) tryAccountLogin(w http.ResponseWriter, r *http.Request, user, pass string) {
	ip := clientIP(r)
	if s.locked(ip) {
		s.renderLogin(w, r, http.StatusTooManyRequests, "Zu viele Fehlversuche. Bitte in ein paar Minuten erneut versuchen.")
		return
	}
	user = strings.TrimSpace(user)
	if user == "" || pass == "" {
		s.renderLogin(w, r, http.StatusBadRequest, "Bitte Benutzername und Passwort eingeben.")
		return
	}
	owner := s.app.AccountUser()
	if owner == "" {
		// NAK_OWNER pins who may claim a fresh instance (public deployments)
		owner = strings.TrimSpace(os.Getenv("NAK_OWNER"))
	}
	if owner != "" && !auth.SameUser(user, owner) {
		s.failed(ip)
		s.renderLogin(w, r, http.StatusUnauthorized, "Diese naknak-Instanz gehört einem anderen NAK-Konto.")
		return
	}
	if owner != "" && s.app.PasswordMatches(user, pass) {
		s.loggedIn(w, r, ip)
		return
	}
	if err := s.app.CheckLogin(user, pass); err != nil {
		if strings.Contains(err.Error(), "wrong credentials") {
			s.failed(ip)
			s.renderLogin(w, r, http.StatusUnauthorized, "Benutzername oder Passwort stimmt nicht.")
			return
		}
		s.renderLogin(w, r, http.StatusBadGateway, "Das CIS ist gerade nicht erreichbar, bitte gleich nochmal versuchen.")
		return
	}
	if s.app.AccountSource() != "env" {
		if err := s.app.SaveAccount(user, pass); err != nil {
			s.renderLogin(w, r, http.StatusInternalServerError, "Konto konnte nicht gespeichert werden: "+err.Error())
			return
		}
		s.store.clear()
	}
	s.loggedIn(w, r, ip)
}
