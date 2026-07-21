// Session is de koppeling met gost-dom: één browservenster dat pagina's
// ophaalt en parset. Scripting staat bewust uit (gost-dom kan Goja/V8
// aanhaken, maar V8 is cgo en Goja een flinke dep — statische pagina's
// eerst). gost-dom lost zelf relatieve hrefs en de history op; wij hoeven
// alleen Navigate te roepen met wat de gebruiker aanklikt of intikt.
package browse

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"errors"
	"image"
	"image/color"
	_ "image/gif" // decoders voor <img>: puur Go, dus ook op tamago
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp" // het halve nieuws-web serveert webp; ook puur Go
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	gost "github.com/gost-dom/browser"
	"github.com/gost-dom/browser/dom"
	"github.com/gost-dom/browser/html"
)

// welkomHTML is de startpagina (geen netwerk nodig): meteen beeld, en een
// mini-zelftest van de layout-engine.
const welkomHTML = `<html><head><title>surf</title></head><body>
<h1>surf browser</h1>
<p>Typ een adres in de balk hierboven en druk op Enter.
Scroll met het wiel, klik links om te volgen.</p>
<hr>
<p><b>gost-dom</b> parset de pagina's; deze layout-engine zet de DOM om in
pixels op het 8x8-font. Geen CSS, geen scripts &mdash; wel <i>leesbaar</i>.</p>
<ul><li>blokken en woordwrap</li><li>koppen op schaal</li>
<li>links: <a href="about:blank">klikbaar</a></li>
<li><code>code</code> en <pre>  pre met  spaties</pre></li></ul>
</body></html>`

// Session is één browservenster.
type Session struct {
	b      *gost.Browser
	win    html.Window
	imgs   map[string]image.Image // gedecodeerde <img>'s van de huidige pagina, op raw src
	styles map[dom.Node]props     // computed CSS-props per element (zie loadStyles)
	edits  map[dom.Node]string    // ingetikte veldwaarden (overleven een re-layout)
	nerr   *netErr                // laatste transportfout uit de netProxy (nil bij handler-tests)
	site   *siteID                // identiteit van de huidige site (kopbalk); nil = geen
}

// siteID is wat een site herkenbaar maakt zónder dat wij zijn logo (SVG)
// kunnen rasteren: het apple-touch-icon (PNG — het icoon dat op een
// telefoon-homescreen komt), de naam (og:site_name of de host) en de
// themakleur (<meta name="theme-color">, de kleur die mobiele browsers om
// de pagina heen tekenen). Samen: het NU.nl-balkje-effect.
type siteID struct {
	icon  image.Image
	name  string
	theme color.RGBA
}

// NewSession start een venster op de ingebouwde startpagina. Het netwerk
// gaat via netProxy: gost-dom's eigen fetch heeft géén timeout, en één dooie
// site zou anders de hele event-lus voorgoed bevriezen ("hij staat vast",
// Derek 19-07 — de browser hing op een niet-antwoordende host).
func NewSession() *Session {
	nerr := &netErr{}
	s := newSession(gost.New(gost.WithHandler(netProxy{nerr}), gost.WithScriptEngine(nopEngine{})))
	s.nerr = nerr
	return s
}

// netErr bewaart de laatste transportfout van de proxy. gost-dom vouwt elke
// niet-200 plat tot "Non-ok Response" (Derek zag letterlijk dat en niets
// meer) — hiermee komt de échte fout ("x509: …", "no such host", "HTTP
// 404") in de statusbalk. De mutex omdat de proxy vanuit gost-dom's fetch
// draait, het uitlezen vanuit Go/Follow.
type netErr struct {
	mu   sync.Mutex
	last string
}

func (e *netErr) set(s string) {
	e.mu.Lock()
	e.last = s
	e.mu.Unlock()
}

func (e *netErr) take() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.last
	e.last = ""
	return s
}

// explain vervangt een kale gost-dom-fout door wat er op de lijn echt
// misging, als de proxy dat weet.
func (s *Session) explain(err error) error {
	if err == nil {
		if s.nerr != nil {
			s.nerr.take() // opruimen: fouten van subresources niet later tonen
		}
		return nil
	}
	if s.nerr != nil {
		if msg := s.nerr.take(); msg != "" {
			return errors.New(msg)
		}
	}
	return err
}

// nopEngine compileert elk script tot een no-op. Niet omdat we JS willen
// draaien, maar omdat gost-dom v0.12 zónder engine panict op de eerste
// pagina met een <script> (window.scriptContext is dan nil — daarom deed
// google.nl het niet terwijl example.com het deed). Goja/V8 kan hier later
// zo in.
type nopEngine struct{}
type nopHost struct{}
type nopContext struct{}
type nopScript struct{}

func (nopEngine) NewHost(html.ScriptEngineOptions) html.ScriptHost { return nopHost{} }
func (nopHost) NewContext(html.BrowsingContext) html.ScriptContext { return nopContext{} }
func (nopHost) Close()                                             {}
func (nopContext) Eval(string) (any, error)                        { return nil, nil }
func (nopContext) Run(string) error                                { return nil }
func (nopContext) Compile(string) (html.Script, error)             { return nopScript{}, nil }
func (nopContext) DownloadScript(string) (html.Script, error)      { return nopScript{}, nil }
func (nopContext) DownloadModule(string) (html.Script, error)      { return nopScript{}, nil }
func (nopContext) Close()                                          {}
func (nopScript) Eval() (any, error)                               { return nil, nil }
func (nopScript) Run() error                                       { return nil }

// netProxy is het "netwerk" voor gost-dom: een http.Handler die de request
// écht uitvoert, met een harde timeout. Zo heeft de hele browser één plek
// voor netbeleid (straks ook: alleen-origin voor gast-apps, §10-vergezicht).
type netProxy struct {
	nerr *netErr
}

// cacert.pem is Mozilla's root-CA-bundel (via https://curl.se/ca/cacert.pem,
// MPL-2.0) — tamago heeft geen certificaatwinkel, dus zonder deze bundel
// faalt élke HTTPS-handshake op het device met "x509: certificate signed by
// unknown authority". Ververs hem af en toe met:
//
//	curl -fsSL https://curl.se/ca/cacert.pem -o browse/cacert.pem
//
//go:embed cacert.pem
var cacertPEM []byte

// netClient met een cookie-jar: consent-muren (DPG's privacy-gate op
// tweakers.net en nu.nl) zetten hun "akkoord"-cookie op een redirect — zonder
// jar kom je eeuwig op de muur terug. Eén jar per proces: dit is een
// éénpersoonsbrowser. cookiejar is pure Go, dus ook op tamago.
var netClient = &http.Client{Timeout: 20 * time.Second, Transport: netTransport(), Jar: newJar()}

func newJar() http.CookieJar {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil // kan met Options=nil niet gebeuren; nil-Jar is gewoon "geen cookies"
	}
	return jar
}

// netTransport: de standaard-transport, met de systeempool waar die bestaat
// (ontwikkelmachine) en anders de meegebakken bundel (bare metal). Let op:
// certificaatverificatie heeft ook een kloppende klok nodig — staat het
// device op epoch, dan is de fout "certificate has expired or is not yet
// valid" en is NTP de echte fix, niet de bundel.
func netTransport() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	pool.AppendCertsFromPEM(cacertPEM)
	t.TLSClientConfig = &tls.Config{RootCAs: pool}
	return t
}

func (p netProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// De inkomende (server-vormige) request omzetten naar een uitgaande.
	out, err := http.NewRequest(r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	out.Header = r.Header.Clone()
	// Cookies zijn van ónze jar (op netClient): wat gost-dom zelf meestuurt
	// weg — twee administraties door elkaar geeft dubbele/verouderde cookies.
	out.Header.Del("Cookie")
	if out.Header.Get("User-Agent") == "" {
		// Wikipedia c.s. weigeren anonieme clients (403); wees gewoon wie
		// we zijn.
		out.Header.Set("User-Agent", "surf/0.1 (HopOS; gost-dom)")
	}
	resp, err := netClient.Do(out)
	if err != nil {
		p.nerr.set(err.Error())
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		p.nerr.set("HTTP " + resp.Status + " " + r.URL.String())
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// NewSessionHandler is NewSession met een in-process http.Handler in plaats
// van het echte netwerk — voor de host-tests, zonder poorten of sockets.
func NewSessionHandler(h http.Handler) *Session {
	return newSession(gost.New(gost.WithHandler(h), gost.WithScriptEngine(nopEngine{})))
}

func newSession(b *gost.Browser) *Session {
	s := &Session{b: b, win: b.NewWindow()}
	s.win.LoadHTML(welkomHTML)
	return s
}

// Go navigeert naar een adresbalk-invoer; een kaal adres ("hop.local",
// "10.0.0.7:8080/status") krijgt http:// ervoor. Bij een fout blijft de
// huidige pagina staan.
func (s *Session) Go(addr string) error {
	if addr == "" {
		return nil
	}
	if !hasScheme(addr) {
		addr = "http://" + addr
	}
	return s.navigate(addr)
}

// Follow navigeert naar een aangeklikte href, onaangeroerd: gost-dom lost
// relatieve paden ("/x", "page2.html", "#anker") tegen de huidige pagina op.
func (s *Session) Follow(href string) error {
	return s.navigate(href)
}

// navigate is de gedeelde landing van Go en Follow: navigeren, een eventuele
// consent-muur één keer automatisch door, en dan de subresources laden.
func (s *Session) navigate(addr string) error {
	err := s.win.Navigate(addr)
	if err == nil {
		// Consent-muur (DPG's privacy-gate: tweakers.net, nu.nl, ...)?
		// Zonder scripts is die pagina letterlijk leeg — de door-URL staat
		// er wel gewoon in. De hop zet het consent-cookie (jar!); daarna
		// opnieuw naar het échte adres, zodat de adresbalk en de base voor
		// relatieve links kloppen. Eén keer, dus nooit een lus; faalt de
		// hop, dan blijft de muur staan en zie je tenminste wáár je bent.
		if gate := consentGateURL(s.win.Document()); gate != "" {
			target := s.win.Location().Href()
			if s.win.Navigate(gate) == nil {
				s.win.Navigate(target)
			}
			if s.nerr != nil {
				s.nerr.take() // fout in de hop: muur tonen, niet klagen
			}
		}
		s.edits = nil // nieuwe pagina, verse velden
		s.loadStyles()
		s.loadImages()
		s.loadSiteID()
	}
	return s.explain(err)
}

// loadSiteID verzamelt icoon, naam en themakleur van de huidige pagina
// (zie siteID). Draait in de nav-goroutine, net als de afbeeldingen.
func (s *Session) loadSiteID() {
	s.site = nil
	base, err := url.Parse(s.win.Location().Href())
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") {
		return
	}
	id := &siteID{name: strings.TrimPrefix(base.Hostname(), "www."), theme: colBar}
	iconHref := ""
	var walk func(n dom.Node)
	walk = func(n dom.Node) {
		if el, ok := n.(dom.Element); ok {
			switch strings.ToLower(el.TagName()) {
			case "link":
				rel, _ := el.GetAttribute("rel")
				rel = strings.ToLower(rel)
				href, _ := el.GetAttribute("href")
				if href == "" {
					break
				}
				if strings.Contains(rel, "apple-touch-icon") {
					iconHref = href // het homescreen-icoon: altijd de beste
				} else if iconHref == "" && strings.Contains(rel, "icon") &&
					(strings.Contains(strings.ToLower(href), ".png") || isPNGType(el)) {
					iconHref = href
				}
			case "meta":
				name, _ := el.GetAttribute("name")
				prop, _ := el.GetAttribute("property")
				content, _ := el.GetAttribute("content")
				switch {
				case strings.EqualFold(name, "theme-color") && content != "":
					if c, ok := cssColor(strings.ToLower(strings.TrimSpace(content))); ok {
						id.theme = c
					}
				case strings.EqualFold(prop, "og:site_name") && strings.TrimSpace(content) != "":
					id.name = strings.TrimSpace(content)
				}
			}
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
	}
	walk(s.win.Document())
	if iconHref == "" {
		iconHref = "/apple-touch-icon.png" // well-known pad: tweakers.net heeft geen link-tag, wél het icoon
	}
	id.icon = s.fetchImage(base, iconHref)
	s.site = id
}

// isPNGType: heeft deze <link> een image/png-type?
func isPNGType(el dom.Element) bool {
	t, _ := el.GetAttribute("type")
	return strings.EqualFold(strings.TrimSpace(t), "image/png")
}

// consentGateURL herkent een consent-muur en geeft de "klik hier verder"-URL
// terug ("" als de pagina geen muur is). Het patroon (DPG Media, het halve
// NL-web): een <script> met decodeURIComponent('https%3A%2F%2F...') waarin
// een privacy-bevestigings-URL met authId zit; die URL GET'en — met de
// cookie-jar — telt als de minimale (functionele-cookies) doorklik.
func consentGateURL(doc dom.Node) string {
	const marker = "decodeURIComponent('"
	found := ""
	var walk func(n dom.Node)
	walk = func(n dom.Node) {
		if found != "" {
			return
		}
		if el, ok := n.(dom.Element); ok && strings.EqualFold(el.TagName(), "script") {
			txt := el.TextContent()
			for i := 0; ; {
				j := strings.Index(txt[i:], marker)
				if j < 0 {
					break
				}
				start := i + j + len(marker)
				end := strings.IndexByte(txt[start:], '\'')
				if end < 0 {
					break
				}
				u, err := url.QueryUnescape(txt[start : start+end])
				if err == nil && hasScheme(u) && strings.Contains(u, "authId=") &&
					strings.Contains(strings.ToLower(u), "privacy") {
					found = u
					return
				}
				i = start + end
			}
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
	}
	if doc != nil {
		walk(doc)
	}
	return found
}

// --- formulieren ------------------------------------------------------------

// Type verwerkt een toets in een invoerveld: een teken erbij, of met bs
// een teken eraf. De waarde leeft in de sessie en overleeft re-layouts.
func (s *Session) Type(f *Field, ch byte, bs bool) {
	if f == nil || f.node == nil {
		return
	}
	if s.edits == nil {
		s.edits = map[dom.Node]string{}
	}
	v, ok := s.edits[f.node]
	if !ok {
		v = f.Value
	}
	if bs {
		if v != "" {
			v = v[:len(v)-1]
		}
	} else {
		v += string(ch)
	}
	s.edits[f.node] = v
	f.Value = v
}

// Submit verstuurt het formulier waar dit veld in zit: alle benoemde
// velden als GET-query op de action-URL (POST is een andere klus — de
// zoekmachines van deze wereld zijn GET). De aangeklikte submit-knop doet
// zijn eigen naam mee, die van de andere knoppen niet.
func (s *Session) Submit(f *Field) error {
	if f == nil || f.node == nil {
		return nil
	}
	form := ancestorForm(f.node)
	if form == nil {
		return nil
	}
	if m, _ := form.GetAttribute("method"); strings.EqualFold(strings.TrimSpace(m), "post") {
		return errors.New("POST-formulier: nog niet gedragen")
	}
	q := url.Values{}
	var collect func(n dom.Node)
	collect = func(n dom.Node) {
		if el, ok := n.(dom.Element); ok && strings.EqualFold(el.TagName(), "input") {
			name, _ := el.GetAttribute("name")
			typ, _ := el.GetAttribute("type")
			typ = strings.ToLower(typ)
			val, _ := el.GetAttribute("value")
			if v, ok := s.edits[n]; ok {
				val = v
			}
			switch {
			case name == "":
			case typ == "submit" || typ == "button" || typ == "image":
				if n == f.node {
					q.Set(name, val)
				}
			case typ == "checkbox" || typ == "radio":
				if _, checked := el.GetAttribute("checked"); checked {
					q.Set(name, val)
				}
			default: // text, search, hidden, ...
				q.Set(name, val)
			}
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			collect(c)
		}
	}
	for c := form.FirstChild(); c != nil; c = c.NextSibling() {
		collect(c)
	}
	action, _ := form.GetAttribute("action")
	if action == "" {
		action = s.URL()
	}
	if i := strings.IndexByte(action, '?'); i >= 0 {
		action = action[:i]
	}
	return s.Follow(action + "?" + q.Encode())
}

// ancestorForm zoekt het omvattende <form>-element.
func ancestorForm(n dom.Node) dom.Element {
	for p := n.ParentElement(); p != nil; p = p.ParentElement() {
		if strings.EqualFold(p.TagName(), "form") {
			return p
		}
	}
	return nil
}

// Grenzen voor het CSS laden en matchen (zelfde gedachte als bij de
// afbeeldingen: bare metal, begrensde heap en begrensde tijd).
const (
	cssMaxSheets = 12        // externe stylesheets per pagina (tweakers: 11, de header-regel zit in #8)
	cssMaxBytes  = 256 << 10 // per sheet, over de lijn
	cssMaxRules  = 4096      // na het wegfilteren van layout-junk en dode pseudo's
	cssBudget    = 5 * time.Second
)

// loadStyles verzamelt de <style>-blokken en <link rel=stylesheet>-sheets,
// parset ze tot regels en rekent per element de computed props uit: elke
// regel één QuerySelectorAll (gost-doms selector-engine), toegepast in
// (specificiteit, bronvolgorde) — later en specifieker wint. Draait in de
// nav-goroutine; de layout doet daarna alleen nog map-lookups.
func (s *Session) loadStyles() {
	s.styles = nil
	doc := s.win.Document()
	base, err := url.Parse(s.win.Location().Href())
	if err != nil {
		return
	}
	var rules []cssRule
	links := 0
	// media=""-attribuut: zelfde evaluatie als @media — een print-sheet of
	// desktop-only sheet doet op ons telefoonvenster niet mee.
	mediaOK := func(el dom.Element) bool {
		m, ok := el.GetAttribute("media")
		if !ok || strings.TrimSpace(m) == "" {
			return true
		}
		return mediaMatches(m, mobileWidth)
	}
	var walk func(n dom.Node)
	walk = func(n dom.Node) {
		if el, ok := n.(dom.Element); ok {
			switch strings.ToLower(el.TagName()) {
			case "style":
				if mediaOK(el) {
					rules = append(rules, parseCSS(n.TextContent(), len(rules))...)
				}
			case "link":
				rel, _ := el.GetAttribute("rel")
				href, _ := el.GetAttribute("href")
				if strings.EqualFold(strings.TrimSpace(rel), "stylesheet") && href != "" && links < cssMaxSheets && mediaOK(el) {
					links++
					rules = append(rules, parseCSS(s.fetchText(base, href), len(rules))...)
				}
			}
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
	}
	walk(doc)
	if len(rules) > cssMaxRules {
		rules = rules[:cssMaxRules]
	}
	if len(rules) == 0 {
		return
	}
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].spec != rules[j].spec {
			return rules[i].spec < rules[j].spec
		}
		return rules[i].seq < rules[j].seq
	})
	styles := map[dom.Node]props{}
	vars := map[string]string{} // custom properties, doc-globaal (versimpeld: geen scoping)
	deadline := time.Now().Add(cssBudget)
	for _, r := range rules {
		if time.Now().After(deadline) {
			break // liever een half gestylede pagina dan een hangende browser
		}
		nodes, err := doc.QuerySelectorAll(r.sel)
		if err != nil || nodes == nil {
			continue // selector die gost-dom niet kent: regel vervalt
		}
		matched := nodes.Length() > 0
		for i := 0; i < nodes.Length(); i++ {
			n := nodes.Item(i)
			p := styles[n]
			if p == nil {
				p = props{}
				styles[n] = p
			}
			for k, v := range r.decls {
				p[k] = v
			}
		}
		if matched {
			// --vars van geraakte regels (:root, body, body.pg-x) gelden
			// doc-globaal in cascade-volgorde — genoeg voor het gangbare
			// "thema op de body"-patroon (gethop.org).
			for k, v := range r.decls {
				if strings.HasPrefix(k, "--") {
					vars[k] = v
				}
			}
		}
	}
	// var(--x) overal oplossen, ook in de vars zelf (--acc: var(--leaf)).
	for k, v := range vars {
		vars[k] = resolveVars(v, vars)
	}
	for _, p := range styles {
		for k, v := range p {
			if strings.Contains(v, "var(") {
				p[k] = resolveVars(v, vars)
			}
		}
	}
	// Kleuren op <html> gelden voor de pagina (html{background:...} is een
	// gangbaar canvas-patroon), maar de layout wandelt vanaf body — schuif
	// ze door naar body waar die ze zelf niet zet.
	if root := doc.DocumentElement(); root != nil {
		if hp := styles[dom.Node(root)]; hp != nil {
			if body := doc.Body(); body != nil {
				bp := styles[dom.Node(body)]
				if bp == nil {
					bp = props{}
					styles[dom.Node(body)] = bp
				}
				for _, k := range []string{"color", "background-color", "background-image"} {
					if _, ok := bp[k]; !ok {
						if v, ok := hp[k]; ok {
							bp[k] = v
						}
					}
				}
			}
		}
	}
	s.styles = styles
}

// fetchText haalt één tekst-subresource (stylesheet) begrensd op; "" bij pech.
func (s *Session) fetchText(base *url.URL, href string) string {
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	resp, err := s.b.Client.Get(base.ResolveReference(ref).String())
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, cssMaxBytes))
	if err != nil {
		return ""
	}
	return string(data)
}

// Grenzen voor het afbeeldingen laden: dit draait straks op bare metal, en
// een pagina vol foto's mag de heap niet opblazen. Boven de kaders → alt-
// tekst, net als bij een laadfout.
const (
	imgMaxCount = 24      // per pagina
	imgMaxBytes = 4 << 20 // per afbeelding, over de lijn
	imgMaxDim   = 2048    // px, per zijde (2048² RGBA = 16MB gedecodeerd)
)

// loadImages haalt de <img src>'s van de huidige pagina op en decodeert ze,
// gesleuteld op het rauwe src-attribuut (waar de layout ze op terugvindt).
// Fouten zijn per afbeelding en stil: de layout valt terug op de alt-tekst.
// Draait in de nav-goroutine, ná Navigate — de event-lus merkt er niets van.
func (s *Session) loadImages() {
	s.imgs = nil
	base, err := url.Parse(s.win.Location().Href())
	if err != nil {
		return
	}
	seen := map[string]bool{}
	load := func(src string) {
		if src == "" || seen[src] || len(seen) >= imgMaxCount {
			return
		}
		seen[src] = true
		if m := s.fetchImage(base, src); m != nil {
			if s.imgs == nil {
				s.imgs = map[string]image.Image{}
			}
			s.imgs[src] = m
		}
	}
	var walk func(n dom.Node)
	walk = func(n dom.Node) {
		if el, ok := n.(dom.Element); ok {
			if strings.EqualFold(el.TagName(), "img") {
				src, _ := el.GetAttribute("src")
				load(src)
			}
			// background-image uit de stylesheets of een inline style —
			// de layout zoekt hem straks op dezelfde sleutel (de rauwe
			// url uit de css) terug.
			if v, ok := s.styles[dom.Node(el)]["background-image"]; ok {
				load(cssURL(v))
			}
			if inline, ok := el.GetAttribute("style"); ok {
				if v, ok := parseDecls(inline)["background-image"]; ok {
					load(cssURL(v))
				}
			}
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
	}
	if body := s.win.Document().Body(); body != nil {
		walk(body)
	}
}

// fetchImage haalt één afbeelding op (src opgelost tegen base) en decodeert
// hem; nil bij elke vorm van pech of buiten de kaders.
func (s *Session) fetchImage(base *url.URL, src string) image.Image {
	ref, err := url.Parse(src)
	if err != nil {
		return nil
	}
	resp, err := s.b.Client.Get(base.ResolveReference(ref).String())
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	// Eerst begrensd binnenhalen, dan op de bytes DecodeConfig → Decode:
	// zo kost een te groot plaatje nooit meer dan imgMaxBytes.
	data, err := io.ReadAll(io.LimitReader(resp.Body, imgMaxBytes))
	if err != nil {
		return nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 ||
		cfg.Width > imgMaxDim || cfg.Height > imgMaxDim {
		return nil
	}
	m, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return m
}

// URL is het adres van de huidige pagina (voor de adresbalk na navigatie).
func (s *Session) URL() string {
	return s.win.Location().Href()
}

// Layout layout de huidige pagina voor deze breedte, inclusief de bij de
// navigatie opgehaalde afbeeldingen, CSS-props en ingetikte veldwaarden.
func (s *Session) Layout(width int) Page {
	return layoutStyled(s.win.Document().Body(), width, s.imgs, s.styles, s.edits, s.site)
}

// hasScheme: "letters://" aan het begin. "host:7878" is géén scheme.
func hasScheme(addr string) bool {
	for i := 0; i < len(addr); i++ {
		c := addr[i]
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			continue
		case c == ':':
			return i > 0 && len(addr) > i+2 && addr[i+1] == '/' && addr[i+2] == '/'
		default:
			return false
		}
	}
	return false
}
