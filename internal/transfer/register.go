package transfer

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Raindancer118/nak-api/internal/client"
	"github.com/Raindancer118/nak-api/internal/exams"
	"github.com/Raindancer118/nak-api/internal/forms"
	"github.com/Raindancer118/nak-api/internal/htmlx"
)

// Registration flow ("neue Transferleistung beantragen"):
//  1. GET action=new            → form posting to action=confirmNew (multipart, Auftragsklärung PDF)
//  2. POST confirmNew           → confirmation page with a form posting to action=create
//  3. POST create               → the binding registration
// Steps 2 and 3 only ever run in Register, i.e. after explicit confirmation.
// Live (14.10.2026) the CIS skipped step 2 and registered on confirmNew,
// answering with a success flash — Register accepts that as done.

const maxUpload = 10 << 20

type NewFormOptions struct {
	Numbers   []forms.Option `json:"bericht_nr"`
	Modules   []forms.Option `json:"modules"`
	Languages []forms.Option `json:"languages"`
	Locked    bool           `json:"sperrvermerk_default"`
	Student   string         `json:"name,omitempty"`
}

type RegisterRequest struct {
	No       string // "5"
	Topic    string
	Module   string // option value (moduleId) or module code like "I162"
	Language string // "de" / "en"
	Locked   *bool  // Sperrvermerk; nil keeps the form default
	FileName string
	File     []byte // Auftragsklärung (PDF)
}

func newForm(c *client.Client) (*forms.Form, error) {
	p, err := c.Page(PagePath)
	if err != nil {
		return nil, err
	}
	a := htmlx.First(htmlx.MustParse(p.Body), htmlx.HrefContains("[action]=new"))
	if a == nil {
		return nil, fmt.Errorf("no 'neue Transferleistung beantragen' link — registration not possible right now")
	}
	p2, err := c.Page(htmlx.AbsURL(c.Base, htmlx.Attr(a, "href")))
	if err != nil {
		return nil, err
	}
	f := forms.FindByAction(forms.Parse(htmlx.Main(htmlx.MustParse(p2.Body)), c.Base), "action]=confirmNew")
	if f == nil {
		return nil, fmt.Errorf("registration form not found: %s", exams.FlashMessage(p2.Body))
	}
	return f, nil
}

func optionsOf(f *forms.Form) *NewFormOptions {
	o := &NewFormOptions{}
	nonEmpty := func(os []forms.Option) []forms.Option {
		var out []forms.Option
		for _, x := range os {
			if x.Value != "" {
				out = append(out, x)
			}
		}
		return out
	}
	if fl := f.Field("transferTermPaper.no"); fl != nil {
		o.Numbers = nonEmpty(fl.Options)
	}
	if fl := f.Field("transferTermPaper.moduleId"); fl != nil {
		o.Modules = nonEmpty(fl.Options)
	}
	if fl := f.Field("transferTermPaper.language"); fl != nil {
		o.Languages = nonEmpty(fl.Options)
	}
	if fl := f.Field("transferTermPaper.locked"); fl != nil {
		o.Locked = fl.Checked
	}
	return o
}

// FetchNewFormOptions shows what the registration form currently offers.
func FetchNewFormOptions(c *client.Client) (*NewFormOptions, error) {
	f, err := newForm(c)
	if err != nil {
		return nil, err
	}
	return optionsOf(f), nil
}

// PrepareRegister validates the request against the live form and builds
// step 1 (confirmNew). Nothing is sent.
func PrepareRegister(c *client.Client, r RegisterRequest) (*forms.Submission, error) {
	f, err := newForm(c)
	if err != nil {
		return nil, err
	}
	return buildRegister(f, r)
}

func buildRegister(f *forms.Form, r RegisterRequest) (*forms.Submission, error) {
	if strings.TrimSpace(r.Topic) == "" {
		return nil, fmt.Errorf("topic (Thema) is required")
	}
	if len(r.File) == 0 {
		return nil, fmt.Errorf("the Auftragsklärung PDF is required")
	}
	if len(r.File) > maxUpload {
		return nil, fmt.Errorf("file is %d bytes; the CIS accepts at most 10 MB", len(r.File))
	}
	if !bytes.HasPrefix(r.File, []byte("%PDF")) {
		return nil, fmt.Errorf("file %s is not a PDF", r.FileName)
	}
	mod, err := resolveModule(optionsOf(f).Modules, r.Module)
	if err != nil {
		return nil, err
	}
	set := map[string]string{
		"transferTermPaper.no":       r.No,
		"transferTermPaper.topic":    strings.TrimSpace(r.Topic),
		"transferTermPaper.moduleId": mod,
	}
	if r.Language != "" {
		set["transferTermPaper.language"] = r.Language
	}
	if r.Locked != nil {
		if *r.Locked {
			set["transferTermPaper.locked"] = "1"
		} else {
			set["transferTermPaper.locked"] = ""
		}
	}
	name := r.FileName
	if name == "" {
		name = "auftragsklaerung.pdf"
	}
	return f.Submit("", set, client.FilePart{Field: "transferTermPaper.registrationFile", Filename: filepath.Base(name), ContentType: "application/pdf", Data: r.File})
}

func resolveModule(opts []forms.Option, want string) (string, error) {
	want = strings.TrimSpace(want)
	for _, o := range opts {
		if o.Value == want || strings.Contains(o.Label, "("+want+")") || strings.EqualFold(o.Label, want) {
			return o.Value, nil
		}
	}
	var avail []string
	for _, o := range opts {
		avail = append(avail, fmt.Sprintf("%s=%s", o.Value, o.Label))
	}
	return "", fmt.Errorf("module %q is not offered; choose one of: %s", want, strings.Join(avail, "; "))
}

// Register sends step 1 and, if the CIS answers with its confirmation form,
// step 2. BINDING. Returns the final page's message.
func Register(c *client.Client, step1 *forms.Submission) (string, error) {
	p, err := step1.Send(c)
	if err != nil {
		return "", fmt.Errorf("confirmNew: %w", err)
	}
	main := htmlx.Main(htmlx.MustParse(p.Body))
	f := forms.FindByAction(forms.Parse(main, c.Base), "action]=create")
	if f == nil {
		if htmlx.First(main, htmlx.Class("alert-success")) != nil {
			return exams.FlashMessage(p.Body), nil
		}
		// Not provably "nothing registered": callers must check the list before retrying.
		return "", fmt.Errorf("the CIS showed neither a confirmation step nor a success message — check the transfer list before retrying: %s", exams.FlashMessage(p.Body))
	}
	s2, err := f.Submit("", nil)
	if err != nil {
		return "", err
	}
	p2, err := s2.Send(c)
	if err != nil {
		return "", fmt.Errorf("create: %w", err)
	}
	return exams.FlashMessage(p2.Body), nil
}
