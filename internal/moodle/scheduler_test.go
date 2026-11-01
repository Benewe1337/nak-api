package moodle

import (
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseSchedulerBooked(t *testing.T) {
	p, err := ParseScheduler(fixture(t, "scheduler_booked.html"), berlin)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Time slots" || len(p.Slots) != 1 {
		t.Fatalf("name=%q slots=%d", p.Name, len(p.Slots))
	}
	s := p.Slots[0]
	if s.Status != "booked" || s.Start != "2026-11-04 16:15" || s.End != "2026-11-04 16:45" || s.Minutes != 30 || s.Weekday != "Mittwoch" {
		t.Errorf("slot = %+v", s)
	}
	if s.Teacher != "Alex Lehrkraft" || s.Location != "" || s.Notes != "" || s.SlotID != 9001 || !s.Cancellable {
		t.Errorf("slot = %+v", s)
	}
	if s.OtherParticipants != 3 {
		t.Errorf("others = %d (own entry must not count)", s.OtherParticipants)
	}
	if !strings.Contains(p.Message, "Buchung war erfolgreich") {
		t.Errorf("message = %q", p.Message)
	}
}

func TestParseSchedulerDoesNotDependOnHeaderLabels(t *testing.T) {
	body := strings.Replace(fixture(t, "scheduler_booked.html"), ">Lehrkraft<", ">Pirie<", 1)
	p, err := ParseScheduler(body, berlin)
	if err != nil || len(p.Slots) != 1 || p.Slots[0].Teacher != "Alex Lehrkraft" {
		t.Fatalf("%v %+v", err, p.Slots)
	}
}

func TestParseSchedulerAvailableAndPast(t *testing.T) {
	p, err := ParseScheduler(fixture(t, "scheduler_available.html"), berlin)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Slots) != 3 {
		t.Fatalf("slots = %+v", p.Slots)
	}
	if s := p.Slots[0]; s.Status != "past" || s.Location != "Raum 2.14" || s.Notes != "Bitte Ausweis mitbringen" || s.Minutes != 20 {
		t.Errorf("past = %+v", s)
	}
	if s := p.Slots[1]; s.Status != "available" || s.SlotID != 9002 || s.Start != "2026-11-05 10:00" || s.Cancellable {
		t.Errorf("available = %+v", s)
	}
}

func TestParseSchedulerEnglishDate(t *testing.T) {
	body := `<html><head><title>x</title></head><body><div role="main"><h3>Upcoming slots</h3><table><thead><tr><th>Date</th><th>Teacher</th></tr></thead>
<tbody><tr><td><div class="datelabel">Wednesday, 4 November 2026</div><div class="timelabel">4:15 PM &ndash; 4:45 PM</div></td><td>X</td></tr></tbody></table></div></body></html>`
	p, err := ParseScheduler(body, berlin)
	if err != nil || len(p.Slots) != 1 || p.Slots[0].Start != "2026-11-04 16:15" || p.Slots[0].End != "2026-11-04 16:45" {
		t.Fatalf("%v %+v", err, p.Slots)
	}
}

func TestParseSchedulerNoBookingsIsEmptyNotError(t *testing.T) {
	body := `<html><head><title>x</title></head><body><div role="main"><div class="alert">Keine Zeitfenster verfügbar</div></div></body></html>`
	p, err := ParseScheduler(body, berlin)
	if err != nil || len(p.Slots) != 0 {
		t.Fatalf("%v %+v", err, p.Slots)
	}
}

func TestParseSchedulerWrongPageIsDrift(t *testing.T) {
	_, err := ParseScheduler(`<html><head><title>Anmeldung</title></head><body><form action="/login/index.php"></form></body></html>`, berlin)
	if err == nil || !errors.Is(err, ErrSchedulerLayout) {
		t.Fatalf("err = %v", err)
	}
}

// fakeWeb is a Moodle web login (logintoken, cookie) plus a scheduler page.
func fakeWeb(t *testing.T, page string) (*httptest.Server, *int32, *int32) {
	var logins, views int32
	mux := http.NewServeMux()
	mux.HandleFunc("/login/index.php", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Write([]byte(`<form action="/login/index.php" method="post"><input type="hidden" name="logintoken" value="LT1"></form>`))
			return
		}
		r.ParseForm()
		if r.PostForm.Get("username") != fakeUser || r.PostForm.Get("password") != fakePass || r.PostForm.Get("logintoken") != "LT1" {
			w.Write([]byte(`<form action="/login/index.php" method="post"><input name="logintoken" value="LT1">bad</form>`))
			return
		}
		atomic.AddInt32(&logins, 1)
		http.SetCookie(w, &http.Cookie{Name: "MoodleSession", Value: "s", Path: "/"})
		http.Redirect(w, r, "/my/", http.StatusSeeOther)
	})
	mux.HandleFunc("/my/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("dashboard")) })
	mux.HandleFunc("/mod/scheduler/view.php", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("scheduler page requested with %s", r.Method)
		}
		if c, err := r.Cookie("MoodleSession"); err != nil || c.Value != "s" {
			http.Redirect(w, r, "/login/index.php", http.StatusSeeOther)
			return
		}
		if r.URL.Query().Get("id") != "315984" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		atomic.AddInt32(&views, 1)
		w.Write([]byte(page))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &logins, &views
}

func TestServiceSchedulerLogsInOnceAndReadsPage(t *testing.T) {
	srv, logins, views := fakeWeb(t, fixture(t, "scheduler_booked.html"))
	s := NewService(NewClient(srv.URL, fakeUser, fakePass), berlin)
	for i := 0; i < 2; i++ {
		r, err := s.Scheduler(315984)
		if err != nil {
			t.Fatal(err)
		}
		if v(t, r, "name") != "Time slots" || v(t, r, "booked", 0, "start") != "2026-11-04 16:15" || v(t, r, "booked", 0, "teacher") != "Alex Lehrkraft" {
			t.Fatalf("result = %+v", r)
		}
		if v(t, r, "url") != srv.URL+"/mod/scheduler/view.php?id=315984" {
			t.Errorf("url = %v", v(t, r, "url"))
		}
	}
	if *logins != 1 || *views != 2 {
		t.Errorf("logins=%d views=%d", *logins, *views)
	}
}

func TestServiceSchedulerReloginAfterSessionExpiry(t *testing.T) {
	srv, logins, _ := fakeWeb(t, fixture(t, "scheduler_booked.html"))
	c := NewClient(srv.URL, fakeUser, fakePass)
	s := NewService(c, berlin)
	if _, err := s.Scheduler(315984); err != nil {
		t.Fatal(err)
	}
	c.web.Jar, _ = cookiejar.New(nil) // session gone: the page redirects to the login
	if _, err := s.Scheduler(315984); err != nil || *logins != 2 {
		t.Fatalf("err=%v logins=%d", err, *logins)
	}
}

func TestServiceSchedulerBadLogin(t *testing.T) {
	srv, _, _ := fakeWeb(t, "")
	s := NewService(NewClient(srv.URL, fakeUser, "wrong"), berlin)
	_, err := s.Scheduler(315984)
	var me *Error
	if !errors.As(err, &me) || me.Code != "login_failed" || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceSchedulerRejectsBadCmid(t *testing.T) {
	s := NewService(NewClient("http://127.0.0.1:1", fakeUser, fakePass), berlin)
	var ae *ArgError
	if _, err := s.Scheduler(0); !errors.As(err, &ae) {
		t.Fatalf("err = %v", err)
	}
}
