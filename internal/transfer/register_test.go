package transfer

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Raindancer118/nak-api/internal/client"
)

// confirmPage is SYNTHETIC (no capture of step 2 exists): a confirmation view
// with a create form, shaped like the other tx_natransfertermpaper forms.
const confirmPage = `<html><body><main><h3>Bitte bestätigen</h3>
<form method="post" action="/studium/bachelor/transferleistungen-praxisberichte?tx_natransfertermpaper_natransferleistungen%5Baction%5D=create&amp;cHash=0">
<input type="hidden" name="tx_natransfertermpaper_natransferleistungen[transferTermPaper][topic]" value="Thema X" />
<input type="submit" name="tx_natransfertermpaper_natransferleistungen[submit]" value="verbindlich beantragen" />
</form></main><a href="/login/?logintype=logout">Logout</a></body></html>`

func read(t *testing.T, n string) string {
	b, err := os.ReadFile("testdata/" + n)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type rec struct {
	method, action, ctype string
	body                  string
}

func fakeTransfer(t *testing.T) (*client.Client, *[]rec) {
	return fakeTransferWith(t, confirmPage)
}

// fakeTransferWith answers step 1 (confirmNew) with the given page.
func fakeTransferWith(t *testing.T, confirmNewPage string) (*client.Client, *[]rec) {
	var mu sync.Mutex
	var log []rec
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		act := r.URL.Query().Get("tx_natransfertermpaper_natransferleistungen[action]")
		mu.Lock()
		log = append(log, rec{r.Method, act, r.Header.Get("Content-Type"), string(b)})
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		switch act {
		case "new":
			io.WriteString(w, read(t, "beantragen.html"))
		case "confirmNew":
			io.WriteString(w, confirmNewPage)
		case "create":
			io.WriteString(w, `<main><div class="typo3-messages"><div class="alert alert-success">Transferleistung wurde beantragt.</div></div></main><a href="/login/?logintype=logout">x</a>`)
		default:
			io.WriteString(w, read(t, "liste-live.html"))
		}
	}))
	t.Cleanup(srv.Close)
	c, _ := client.NewWith(client.Options{BaseURL: srv.URL, SessionDir: t.TempDir()})
	return c, &log
}

func TestRegisterFlow(t *testing.T) {
	c, log := fakeTransfer(t)
	opts, err := FetchNewFormOptions(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Numbers) != 2 || opts.Numbers[0].Value != "5" || len(opts.Modules) < 10 || !opts.Locked {
		t.Fatalf("options = %+v", opts)
	}
	req := RegisterRequest{No: "5", Topic: "Thema X", Module: "358", Language: "de", FileName: "/tmp/a.pdf", File: []byte("%PDF-1.4 test")}
	s, err := PrepareRegister(c, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range *log {
		if r.method != http.MethodGet {
			t.Fatalf("prepare sent %s %s", r.method, r.action)
		}
	}
	if !s.Multipart || len(s.Files) != 1 || s.Files[0].Filename != "a.pdf" {
		t.Errorf("submission = %+v", s)
	}
	if !strings.Contains(s.Preview(), "Thema X") {
		t.Errorf("preview = %s", s.Preview())
	}

	msg, err := Register(c, s)
	if err != nil || msg != "Transferleistung wurde beantragt." {
		t.Fatalf("register = %q %v", msg, err)
	}
	var posts []rec
	for _, r := range *log {
		if r.method == http.MethodPost {
			posts = append(posts, r)
		}
	}
	if len(posts) != 2 || posts[0].action != "confirmNew" || posts[1].action != "create" {
		t.Fatalf("posts = %+v", posts)
	}
	if !strings.HasPrefix(posts[0].ctype, "multipart/form-data") || !strings.Contains(posts[0].body, "%PDF-1.4 test") ||
		!strings.Contains(posts[0].body, "Thema X") || !strings.Contains(posts[0].body, `name="tx_natransfertermpaper_natransferleistungen[transferTermPaper][registrationFile][]"; filename="a.pdf"`) {
		t.Errorf("step 1 body:\n%s", posts[0].body)
	}
}

func TestRegisterValidation(t *testing.T) {
	c, _ := fakeTransfer(t)
	base := RegisterRequest{No: "5", Topic: "T", Module: "358", File: []byte("%PDF")}
	cases := map[string]func(*RegisterRequest){
		"no topic":   func(r *RegisterRequest) { r.Topic = " " },
		"no pdf":     func(r *RegisterRequest) { r.File = []byte("hello") },
		"bad module": func(r *RegisterRequest) { r.Module = "X999" },
		"bad number": func(r *RegisterRequest) { r.No = "1" },
		"too large":  func(r *RegisterRequest) { r.File = append([]byte("%PDF"), make([]byte, maxUpload)...) },
	}
	for name, mut := range cases {
		r := base
		mut(&r)
		if _, err := PrepareRegister(c, r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestBewertungFollowsSignedLink(t *testing.T) {
	c, log := fakeTransfer(t)
	if _, err := FetchBewertung(c, "14534"); err != nil {
		t.Fatal(err)
	}
	last := (*log)[len(*log)-1]
	if last.action != "performance" {
		t.Errorf("last request = %+v", last)
	}
	if _, err := FetchBewertung(c, "1"); err == nil {
		t.Error("unknown id accepted")
	}
}

// Live behaviour seen on 14.10.2026: the CIS skipped the confirmation view and
// registered on confirmNew, answering with the list plus a success flash.
const directSuccessPage = `<html><body><main><div class="typo3-messages"><div class="alert alert-success" role="alert">
<div class="media"><div class="media-body"><h4 class="alert-title">Erfolg:</h4>
<p class="alert-message">Antrag zur 5. Transferleistung ist eingegangen.</p></div></div></div></div>
<table><tr><td>5</td></tr></table></main><a href="/login/?logintype=logout">Logout</a></body></html>`

const directErrorPage = `<html><body><main><div class="typo3-messages"><div class="alert alert-danger" role="alert">
<h4 class="alert-title">Fehler:</h4><p class="alert-message">Bitte laden Sie eine Auftragsklärung hoch.</p></div></div>
</main><a href="/login/?logintype=logout">Logout</a></body></html>`

func registerOnce(t *testing.T, page string) (string, error, []rec) {
	c, log := fakeTransferWith(t, page)
	s, err := PrepareRegister(c, RegisterRequest{No: "5", Topic: "Thema X", Module: "358", File: []byte("%PDF-1.4 test")})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := Register(c, s)
	var posts []rec
	for _, r := range *log {
		if r.method == http.MethodPost {
			posts = append(posts, r)
		}
	}
	return msg, err, posts
}

func TestRegisterDirectSuccessWithoutConfirmStep(t *testing.T) {
	msg, err, posts := registerOnce(t, directSuccessPage)
	if err != nil {
		t.Fatalf("success page reported as error: %v", err)
	}
	if !strings.Contains(msg, "Antrag zur 5. Transferleistung ist eingegangen") {
		t.Errorf("msg = %q", msg)
	}
	if len(posts) != 1 || posts[0].action != "confirmNew" {
		t.Fatalf("posts = %+v", posts)
	}
}

func TestRegisterDirectErrorStaysError(t *testing.T) {
	_, err, posts := registerOnce(t, directErrorPage)
	if err == nil || !strings.Contains(err.Error(), "Auftragsklärung") {
		t.Fatalf("err = %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts = %+v", posts)
	}
}
