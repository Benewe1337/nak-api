// Package transfer reads and submits Transferleistungen / Praxisberichte.
//
// Endpoint: /studium/bachelor/transferleistungen-praxisberichte
// TYPO3 extension tx_natransfertermpaper, controller TransferTermPaper.
//
// Read actions:
//   - list        -> overview of the student's reports (FetchList)
//   - performance -> grading detail of one report (FetchBewertung)
//   - document    -> download an attached file (Download)
//
// Write action (registering a new report) lives in register.go.
package transfer

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Raindancer118/nak-api/internal/client"
	"github.com/Raindancer118/nak-api/internal/htmlx"
	"golang.org/x/net/html"
)

// PagePath is the Transferleistungen overview.
const PagePath = "/studium/bachelor/transferleistungen-praxisberichte"

// Report is one entry in the Transferleistungen overview (list view).
type Report struct {
	ID             string      `json:"id"`             // transferTermPaperId (from the action link)
	No             string      `json:"no"`             // Nr.
	Abgabedatum    string      `json:"abgabedatum"`    // spät. Abgabedatum
	Korrekturfrist string      `json:"korrekturfrist"` // Korrekturfristende
	Topic          string      `json:"topic"`          // Thema
	Module         string      `json:"module"`         // Modul (with code)
	Wertung        string      `json:"wertung"`        // e.g. "bestanden"
	Versuch        string      `json:"versuch"`        // attempt number
	Status         string      `json:"status"`         // e.g. "bewertet"
	Action         string      `json:"action"`         // first action ("performance", "upload", …)
	ActionURL      string      `json:"action_url"`     // cHash-signed link from the page
	Actions        []RowAction `json:"actions,omitempty"`
	Running        bool        `json:"running,omitempty"` // IsRunning: writing time runs, not submitted yet
}

// IsRunning: registered and its writing time runs, not submitted yet — the CIS says
// "Erstellungszeit läuft" and offers the upload; graded or submitted ones are not running.
func (r Report) IsRunning() bool {
	st := strings.ToLower(r.Status)
	if strings.Contains(st, "bewertet") || strings.Contains(st, "abgegeben") || strings.Contains(st, "eingereicht") || r.Wertung != "" {
		return false
	}
	if strings.Contains(st, "erstellungszeit") || strings.Contains(st, "läuft") {
		return true
	}
	if r.Action == "upload" {
		return true
	}
	for _, a := range r.Actions {
		if a.Action == "upload" {
			return true
		}
	}
	return false
}

// RowAction is one icon link of a report row.
type RowAction struct {
	Action string `json:"action"`
	Label  string `json:"label"`
	URL    string `json:"url"`
}

// Bewertung is the grading detail (performance view) of one report.
type Bewertung struct {
	ID        string            `json:"id"`        // transferTermPaperId
	Fields    map[string]string `json:"fields"`    // header label -> value (Matrikel-Nr., Thema, Modul, …)
	Kriterien []Kriterium       `json:"kriterien"` // per-criterion grading
	Documents []Document        `json:"documents"` // downloadable attachments
	// Gesamtnote is a weighted average of the per-criterion notes, computed
	// client-side (Σ note·weight / Σ weight). The CIS itself shows no overall
	// grade — see HasGesamtnote.
	Gesamtnote    float64 `json:"gesamtnote"`
	HasGesamtnote bool    `json:"has_gesamtnote"` // false if no weighted notes were parseable
}

// Kriterium is one row of the Bewertungskriterien table.
type Kriterium struct {
	Kriterium  string `json:"kriterium"`
	Note       string `json:"note"`
	Gewichtung string `json:"gewichtung"`
	Feedback   string `json:"feedback"`
}

// Document is a downloadable file attached to a report.
type Document struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// FetchList fetches the Transferleistungen overview and returns all reports.
func FetchList(c *client.Client) ([]Report, error) {
	p, err := c.Page(PagePath)
	if err != nil {
		return nil, fmt.Errorf("fetch transfer list: %w", err)
	}
	return parseList(p.Body, c.Base), nil
}

// FetchBewertung follows the cHash-signed "performance" link of the report
// from the live overview (TYPO3 rejects links without a valid cHash).
func FetchBewertung(c *client.Client, transferTermPaperID string) (*Bewertung, error) {
	list, err := FetchList(c)
	if err != nil {
		return nil, err
	}
	link := ""
	for _, r := range list {
		for _, a := range r.Actions {
			if r.ID == transferTermPaperID && a.Action == "performance" {
				link = a.URL
			}
		}
	}
	if link == "" {
		return nil, fmt.Errorf("Transferleistung %s has no grading view (not graded yet or unknown id)", transferTermPaperID)
	}
	p, err := c.Page(link)
	if err != nil {
		return nil, fmt.Errorf("fetch performance: %w", err)
	}
	b := parseBewertung(p.Body, c.Base)
	b.ID = transferTermPaperID
	return b, nil
}

// Download fetches a document attachment and returns its bytes plus content type.
func Download(c *client.Client, downloadURL string) ([]byte, string, error) {
	data, ct, _, err := c.Download(downloadURL)
	return data, ct, err
}

func parseBewertung(body, base string) *Bewertung {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return &Bewertung{Fields: map[string]string{}}
	}
	b := &Bewertung{Fields: map[string]string{}}

	// Header: Bootstrap form-group rows -> <label>Key:</label> ... <span>Value</span>
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" && strings.Contains(attr(n, "class"), "form-group") {
			if k, v, ok := parseFormGroup(n); ok {
				if _, exists := b.Fields[k]; !exists {
					b.Fields[k] = v
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	b.Kriterien = parseKriterien(doc)
	b.Documents = parseDocuments(doc, base)
	b.Gesamtnote, b.HasGesamtnote = WeightedAverage(b.Kriterien)
	return b
}

// WeightedAverage computes Σ(note·weight) / Σ(weight) over the criteria that
// carry a parseable note and weight. The CIS does not publish an overall grade;
// this is a client-side convenience. Returns ok=false when nothing was usable.
func WeightedAverage(ks []Kriterium) (float64, bool) {
	var sum, wsum float64
	for _, k := range ks {
		note, ok1 := parseGermanFloat(k.Note)
		weight, ok2 := parsePercent(k.Gewichtung)
		if !ok1 || !ok2 || weight == 0 {
			continue
		}
		sum += note * weight
		wsum += weight
	}
	if wsum == 0 {
		return 0, false
	}
	return sum / wsum, true
}

// parseGermanFloat parses "2,0" or "2.0" into 2.0.
func parseGermanFloat(s string) (float64, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// parsePercent parses "10 %" or "10%" into 10.
func parsePercent(s string) (float64, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "%", ""))
	return parseGermanFloat(s)
}

// parseList parses the Transferleistungen overview table.
func parseList(body, base string) []Report {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}

	// The overview is the table whose header carries "Thema" and "Korrekturfristende".
	var table *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if table != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "table" {
			head := strings.ToLower(innerText(n))
			if strings.Contains(head, "thema") && strings.Contains(head, "korrekturfrist") {
				table = n
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if table == nil {
		return nil
	}

	var reports []Report
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			if r, ok := parseListRow(n, base); ok {
				r.Running = r.IsRunning()
				reports = append(reports, r)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(table)
	return reports
}

func parseListRow(tr *html.Node, base string) (Report, bool) {
	var cells []*html.Node
	for td := tr.FirstChild; td != nil; td = td.NextSibling {
		if td.Type == html.ElementNode && td.Data == "td" {
			cells = append(cells, td)
		}
	}
	// Nr | Abgabedatum | Korrekturfrist | Thema | Modul | Wertung | Versuch | Status | Aktion
	if len(cells) < 9 {
		return Report{}, false
	}
	r := Report{
		No:             textOf(cells[0]),
		Abgabedatum:    textOf(cells[1]),
		Korrekturfrist: textOf(cells[2]),
		Topic:          textOf(cells[3]),
		Module:         textOf(cells[4]),
		Wertung:        textOf(cells[5]),
		Versuch:        textOf(cells[6]),
		Status:         textOf(cells[7]),
	}
	for _, a := range htmlx.All(cells[8], htmlx.Tag("a")) {
		href := htmlx.AbsURL(base, attr(a, "href"))
		act := queryParam(href, "action")
		if act == "" {
			continue
		}
		label := ""
		if sp := htmlx.First(a, func(n *html.Node) bool { return htmlx.Attr(n, "title") != "" }); sp != nil {
			label = htmlx.Attr(sp, "title")
		}
		r.Actions = append(r.Actions, RowAction{Action: act, Label: label, URL: href})
		if r.ActionURL == "" {
			r.ActionURL, r.Action = href, act
			r.ID = queryParam(href, "transferTermPaperId")
		}
	}
	if r.No == "" && r.Topic == "" {
		return Report{}, false
	}
	return r, true
}

func firstAnchor(n *html.Node) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" && attr(n, "href") != "" {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}

func queryParam(rawURL, param string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	for k, v := range u.Query() {
		if (k == param || strings.HasSuffix(k, "["+param+"]")) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

// parseFormGroup extracts the label text (without trailing colon) and the value
// span text from a Bootstrap horizontal form-group.
func parseFormGroup(n *html.Node) (string, string, bool) {
	var label, value string
	var foundLabel bool
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "label":
				if !foundLabel {
					label = textOf(n)
					foundLabel = true
				}
			case "span":
				if value == "" {
					value = textOf(n)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	label = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(label), ":"))
	if label == "" || value == "" {
		return "", "", false
	}
	return label, value, true
}

// parseKriterien finds the Bewertungskriterien table (a 4-column table whose
// header includes "Note" and "Gewichtung") and returns its data rows.
func parseKriterien(doc *html.Node) []Kriterium {
	var out []Kriterium
	var table *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if table != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "table" {
			head := strings.ToLower(innerText(n))
			if strings.Contains(head, "gewichtung") && strings.Contains(head, "feedback") {
				table = n
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if table == nil {
		return nil
	}

	var walkRows func(*html.Node)
	walkRows = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			var cells []string
			for td := n.FirstChild; td != nil; td = td.NextSibling {
				if td.Type == html.ElementNode && td.Data == "td" {
					cells = append(cells, textOf(td))
				}
			}
			// Skip header rows (rendered with <th>) and the explanatory first cell.
			if len(cells) == 4 && cells[0] != "" && cells[1] != "" {
				out = append(out, Kriterium{
					Kriterium:  cells[0],
					Note:       cells[1],
					Gewichtung: cells[2],
					Feedback:   cells[3],
				})
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walkRows(c)
		}
	}
	walkRows(table)
	return out
}

func parseDocuments(doc *html.Node, base string) []Document {
	var docs []Document
	seen := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			href := attr(n, "href")
			if strings.Contains(href, "action%5D=document") || strings.Contains(href, "action]=document") {
				u := htmlx.AbsURL(base, href)
				if !seen[u] {
					seen[u] = true
					label := textOf(n)
					if label == "" {
						label = "Dokument"
					}
					docs = append(docs, Document{Label: label, URL: u})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return docs
}

// ── shared helpers ──────────────────────────────────────────────────────────

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	return strings.Join(strings.Fields(innerText(n)), " ")
}

func innerText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}
