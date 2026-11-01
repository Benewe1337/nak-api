package moodle

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// moodleSched mimics the student side of mod/scheduler: web login with
// logintoken, a per-login sesskey, the booking page rendered from state and
// bookslot/cancelbooking POSTs that redirect back to the page (303), as
// studentview.controller.php does. Behaviour follows upstream; only the cancel
// form markup was captured live.
type moodleSched struct {
	mu       sync.Mutex
	srv      *httptest.Server
	t        *testing.T
	sesskey  string
	logins   int
	posts    []map[string]string
	gets     int
	booked   map[int64]bool // own bookings
	taken    map[int64]bool // slots full for others
	limit    int            // max own bookings
	noCancel bool           // booking period over
	flash    string         // error shown after the next redirect
	sessions int
	cookie   string
	// one-shot faults injected when the next POST arrives
	expireOnPost, rotateOnPost bool
	takeOnPost                 int64
}

var slotDefs = []struct {
	id         int64
	date, from string
	to         string
}{{9001, "Mittwoch, 4. November 2026", "16:15", "16:45"}, {9002, "Donnerstag, 5. November 2026", "10:00", "10:30"}, {9003, "Donnerstag, 5. November 2026", "10:30", "11:00"}}

func newMoodleSched(t *testing.T, own ...int64) *moodleSched {
	f := &moodleSched{t: t, booked: map[int64]bool{}, taken: map[int64]bool{}, limit: 1, sesskey: "SK0", cookie: "C0"}
	for _, id := range own {
		f.booked[id] = true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/login/index.php", f.login)
	mux.HandleFunc("/my/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("dashboard")) })
	mux.HandleFunc("/mod/scheduler/view.php", f.view)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *moodleSched) service() (*Service, *Client) {
	c := NewClient(f.srv.URL, fakeUser, fakePass)
	return NewService(c, berlin), c
}

func (f *moodleSched) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		w.Write([]byte(`<form method="post"><input type="hidden" name="logintoken" value="LT1"></form>`))
		return
	}
	r.ParseForm()
	if r.PostForm.Get("username") != fakeUser || r.PostForm.Get("password") != fakePass || r.PostForm.Get("logintoken") != "LT1" {
		w.Write([]byte(`<form method="post"><input name="logintoken" value="LT1">bad</form>`))
		return
	}
	f.mu.Lock()
	f.logins++
	f.sessions++
	f.sesskey = fmt.Sprintf("SK%d", f.sessions)
	f.cookie = fmt.Sprintf("C%d", f.sessions)
	ck := f.cookie
	f.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "MoodleSession", Value: ck, Path: "/"})
	http.Redirect(w, r, "/my/", http.StatusSeeOther)
}

func (f *moodleSched) form(what string, id int64) string {
	return fmt.Sprintf(`<form method="post" action="%s/mod/scheduler/view.php"><input type="hidden" name="id" value="315984"><input type="hidden" name="sesskey" value="%s"><input type="hidden" name="what" value="%s"><input type="hidden" name="slotid" value="%d"><button type="submit">x</button></form>`,
		f.srv.URL, f.sesskey, what, id)
}

func (f *moodleSched) page() string {
	var b strings.Builder
	b.WriteString(`<html><head><title>Orals: Time slots | M</title></head><body><div class="activity-header"><h1>Time slots</h1></div><div role="main">`)
	if f.flash != "" {
		b.WriteString(`<div class="alert alert-danger" role="alert">` + html.EscapeString(f.flash) + `</div>`)
		f.flash = ""
	}
	own := 0
	var rows, avail strings.Builder
	for _, d := range slotDefs {
		date := fmt.Sprintf(`<td><div class="datelabel">%s</div><div class="timelabel">%s &ndash; %s</div></td>`, d.date, d.from, d.to)
		if f.booked[d.id] {
			own++
			cancel := ""
			if !f.noCancel {
				cancel = f.form("cancelbooking", d.id)
			}
			rows.WriteString(`<tr>` + date + `<td><a href="/user/view.php?id=2001&amp;course=42">Alex Lehrkraft</a></td><td></td><td></td><td>` + cancel + `</td></tr>`)
		}
	}
	if own > 0 {
		b.WriteString(`<h3>Aktuelle Zeitfenster</h3><table><thead><tr><th>Datum</th><th>Lehrkraft</th><th>Ort</th><th>Kommentare</th><td></td></tr></thead><tbody>` + rows.String() + `</tbody></table>`)
	}
	if own >= f.limit {
		b.WriteString(`<div class="studentbookingmessage">Sie können keine weiteren Termine buchen.</div>`)
	} else {
		for _, d := range slotDefs {
			if f.booked[d.id] {
				continue
			}
			btn := f.form("bookslot", d.id)
			if f.taken[d.id] {
				btn = ""
			}
			avail.WriteString(fmt.Sprintf(`<tr><td>%s</td><td>%s</td><td>%s</td><td></td><td></td><td><a href="/user/view.php?id=2001&amp;course=42">Alex Lehrkraft</a></td><td>Nein</td><td>%s</td></tr>`, d.date, d.from, d.to, btn))
		}
		b.WriteString(`<h3>Verfügbare Zeitfenster</h3><table id="slotbookertable"><thead><tr><th>Datum</th><th>Beginn</th><th>Ende</th><th>Ort</th><th>Kommentare</th><th>Lehrkraft</th><th>Gruppe</th><th></th></tr></thead><tbody>` + avail.String() + `</tbody></table>`)
	}
	b.WriteString(`</div></body></html>`)
	return b.String()
}

func (f *moodleSched) view(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, err := r.Cookie("MoodleSession")
	if err != nil || c.Value != f.cookie {
		http.Redirect(w, r, "/login/index.php", http.StatusSeeOther)
		return
	}
	if r.Method == "POST" {
		if f.expireOnPost {
			f.expireOnPost = false
			f.cookie = "gone"
			http.Redirect(w, r, "/login/index.php", http.StatusSeeOther)
			return
		}
		if f.rotateOnPost {
			f.rotateOnPost = false
			f.sesskey = "ROTATED"
		}
		if f.takeOnPost != 0 {
			f.taken[f.takeOnPost] = true
		}
		r.ParseForm()
		rec := map[string]string{}
		for k, v := range r.PostForm {
			rec[k] = v[0]
		}
		f.posts = append(f.posts, rec)
		if rec["sesskey"] != f.sesskey {
			w.Write([]byte(`<html><body><div role="main"><div class="alert alert-danger">Ihr Sitzungsschlüssel ist ungültig (invalidsesskey)</div></div></body></html>`))
			return
		}
		slot, _ := strconv.ParseInt(rec["slotid"], 10, 64)
		switch rec["what"] {
		case "bookslot":
			own := 0
			for _, v := range f.booked {
				if v {
					own++
				}
			}
			switch {
			case own >= f.limit:
				f.flash = "Sie haben zu viele Termine gewählt"
			case f.taken[slot]:
				f.flash = "Dieses Zeitfenster wurde gerade von jemand anderem gebucht"
			default:
				f.booked[slot] = true
			}
		case "cancelbooking":
			if !f.noCancel {
				delete(f.booked, slot)
			}
		}
		http.Redirect(w, r, "/mod/scheduler/view.php?id=315984", http.StatusSeeOther)
		return
	}
	f.gets++
	w.Write([]byte(f.page()))
}

func (f *moodleSched) lastPost() map[string]string {
	if len(f.posts) == 0 {
		return nil
	}
	return f.posts[len(f.posts)-1]
}

func argMsg(t *testing.T, err error, want string) {
	t.Helper()
	var ae *ArgError
	if !errors.As(err, &ae) || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want ArgError containing %q", err, want)
	}
}

func TestCancelPreviewShowsRequestAndSendsNothing(t *testing.T) {
	f := newMoodleSched(t, 9001)
	s, _ := f.service()
	r, err := s.SchedulerCancel(315984, 9001, false)
	if err != nil {
		t.Fatal(err)
	}
	if v(t, r, "mode") != "preview" || v(t, r, "method") != "POST" || v(t, r, "url") != f.srv.URL+"/mod/scheduler/view.php" {
		t.Fatalf("preview = %+v", r)
	}
	if v(t, r, "fields", "what") != "cancelbooking" || v(t, r, "fields", "slotid") != "9001" || v(t, r, "fields", "id") != "315984" {
		t.Errorf("fields = %+v", v(t, r, "fields"))
	}
	if v(t, r, "fields", "sesskey") != nil {
		t.Error("sesskey must not be shown")
	}
	if v(t, r, "slot", "start") != "2026-11-04 16:15" || v(t, r, "slot", "teacher") != "Alex Lehrkraft" {
		t.Errorf("slot = %+v", v(t, r, "slot"))
	}
	if len(f.posts) != 0 || !f.booked[9001] {
		t.Fatal("dry run changed something")
	}
}

func TestCancelConfirmedSendsFormFromPageAndVerifies(t *testing.T) {
	f := newMoodleSched(t, 9001)
	s, _ := f.service()
	r, err := s.SchedulerCancel(315984, 9001, true)
	if err != nil {
		t.Fatal(err)
	}
	p := f.lastPost()
	if len(f.posts) != 1 || p["what"] != "cancelbooking" || p["slotid"] != "9001" || p["id"] != "315984" || p["sesskey"] != f.sesskey {
		t.Fatalf("posts = %+v", f.posts)
	}
	if v(t, r, "mode") != "sent" || f.booked[9001] {
		t.Errorf("result = %+v booked=%v", r, f.booked)
	}
}

func TestCancelErrors(t *testing.T) {
	f := newMoodleSched(t, 9001)
	s, _ := f.service()
	_, err := s.SchedulerCancel(315984, 4242, true)
	argMsg(t, err, "not one of your bookings")
	_, err = s.SchedulerCancel(315984, 9002, false)
	argMsg(t, err, "not one of your bookings")
	f.noCancel = true
	_, err = s.SchedulerCancel(315984, 9001, true)
	argMsg(t, err, "without a cancel button")
	if len(f.posts) != 0 {
		t.Fatalf("nothing may be sent: %+v", f.posts)
	}
}

func TestBookPreviewAndConfirm(t *testing.T) {
	f := newMoodleSched(t)
	s, _ := f.service()
	r, err := s.SchedulerBook(315984, 9002, false)
	if err != nil {
		t.Fatal(err)
	}
	if v(t, r, "mode") != "preview" || v(t, r, "fields", "what") != "bookslot" || v(t, r, "fields", "slotid") != "9002" ||
		v(t, r, "slot", "start") != "2026-11-05 10:00" || v(t, r, "fields", "sesskey") != nil {
		t.Fatalf("preview = %+v", r)
	}
	if len(f.posts) != 0 || len(f.booked) != 0 {
		t.Fatal("dry run changed something")
	}
	r, err = s.SchedulerBook(315984, 9002, true)
	if err != nil {
		t.Fatal(err)
	}
	if p := f.lastPost(); p["what"] != "bookslot" || p["slotid"] != "9002" || p["sesskey"] != f.sesskey || !f.booked[9002] {
		t.Fatalf("post = %+v booked=%v", f.posts, f.booked)
	}
	if v(t, r, "mode") != "sent" || v(t, r, "slot", "start") != "2026-11-05 10:00" {
		t.Errorf("result = %+v", r)
	}
}

func TestBookErrors(t *testing.T) {
	f := newMoodleSched(t)
	s, _ := f.service()
	_, err := s.SchedulerBook(315984, 4242, true)
	argMsg(t, err, "9002")
	f.taken[9003] = true
	_, err = s.SchedulerBook(315984, 9003, true)
	argMsg(t, err, "not among the bookable")

	f2 := newMoodleSched(t, 9001) // limit reached: Moodle lists no bookable slots at all
	s2, _ := f2.service()
	_, err = s2.SchedulerBook(315984, 9002, true)
	argMsg(t, err, "keine weiteren Termine")
	_, err = s2.SchedulerBook(315984, 9001, true)
	argMsg(t, err, "already booked")
	if len(f.posts)+len(f2.posts) != 0 {
		t.Fatal("nothing may be sent")
	}
}

func TestBookRejectedByMoodleAfterRace(t *testing.T) {
	f := newMoodleSched(t)
	s, _ := f.service()
	f.takeOnPost = 9002 // looks free on the page we read, gone when the POST arrives
	_, err := s.SchedulerBook(315984, 9002, true)
	var me *Error
	if !errors.As(err, &me) || me.Code != "scheduler_rejected" || !strings.Contains(me.Msg, "gerade von jemand anderem") {
		t.Fatalf("err = %v", err)
	}
	if f.booked[9002] {
		t.Error("booked despite rejection")
	}
}

func TestWriteBlockedInReadOnlyMode(t *testing.T) {
	f := newMoodleSched(t, 9001)
	s, c := f.service()
	c.ReadOnly = true
	if _, err := s.SchedulerCancel(315984, 9001, true); !errors.Is(err, ErrReadOnly) {
		t.Errorf("cancel: %v", err)
	}
	f2 := newMoodleSched(t)
	s2, c2 := f2.service()
	c2.ReadOnly = true
	if _, err := s2.SchedulerBook(315984, 9002, true); !errors.Is(err, ErrReadOnly) {
		t.Errorf("book: %v", err)
	}
	if len(f.posts)+len(f2.posts) != 0 || !f.booked[9001] || f2.booked[9002] {
		t.Fatal("read-only mode let a write through")
	}
	// the preview is a plain GET and still works
	if r, err := s.SchedulerCancel(315984, 9001, false); err != nil || v(t, r, "mode") != "preview" {
		t.Fatalf("preview in read-only: %v %+v", err, r)
	}
}

func TestWriteReloginAfterSessionExpiry(t *testing.T) {
	f := newMoodleSched(t, 9001)
	s, _ := f.service()
	f.expireOnPost = true // the server forgets the session after we read the page
	if _, err := s.SchedulerCancel(315984, 9001, true); err != nil {
		t.Fatal(err)
	}
	if f.booked[9001] || f.logins != 2 || len(f.posts) != 1 {
		t.Errorf("booked=%v logins=%d posts=%d", f.booked, f.logins, len(f.posts))
	}
	if p := f.lastPost(); p["sesskey"] != f.sesskey {
		t.Errorf("post used a stale sesskey: %+v (current %s)", p, f.sesskey)
	}
}

func TestSesskeyMismatchIsReported(t *testing.T) {
	f := newMoodleSched(t, 9001)
	s, _ := f.service()
	f.rotateOnPost = true
	_, err := s.SchedulerCancel(315984, 9001, true)
	var me *Error
	if !errors.As(err, &me) || me.Code != "invalid_sesskey" {
		t.Fatalf("err = %v", err)
	}
	if !f.booked[9001] {
		t.Error("booking vanished")
	}
}

func TestRejectsForeignOrOddFormTargets(t *testing.T) {
	body := strings.ReplaceAll(fixture(t, "scheduler_booked.html"), "https://moodle.example.org/mod/scheduler/view.php", "https://evil.example.net/mod/scheduler/view.php")
	srv, _, _ := fakeWeb(t, body)
	s := NewService(NewClient(srv.URL, fakeUser, fakePass), berlin)
	_, err := s.SchedulerCancel(315984, 9001, true)
	if err == nil || !strings.Contains(err.Error(), "other host") {
		t.Fatalf("err = %v", err)
	}
}

func TestBookRefusesBookingFormSchedulers(t *testing.T) {
	body := strings.ReplaceAll(fixture(t, "scheduler_available.html"), `value="bookslot"`, `value="bookingform"`)
	srv, _, _ := fakeWeb(t, body)
	s := NewService(NewClient(srv.URL, fakeUser, fakePass), berlin)
	_, err := s.SchedulerBook(315984, 9002, true)
	argMsg(t, err, "booking details")
}
