// Session is het browservenster: pagina's ophalen, parsen en klaarzetten
// voor de layout. De DOM is golang.org/x/net/html (de WHATWG-parser die
// heel Go-land gebruikt), selectors matcht cascadia — allebei puur Go, dus
// ook op tamago. Er zit bewust géén browserframework meer tussen: wij zijn
// zelf de browser, en scripting bestaat hier niet (statische pagina's +
// mechanisme-detectie, zie consentGateURL en de sr-hidden-regels).
package browse

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"fmt"
	"image"
	_ "image/gif" // decoders voor <img>: puur Go, dus ook op tamago
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	_ "golang.org/x/image/webp" // het halve nieuws-web serveert webp; ook puur Go
)

// welkomHTML is de startpagina (geen netwerk nodig): meteen beeld, en een
// mini-zelftest van de layout-engine.
const welkomHTML = `<html><head><title>surf</title></head><body>
<h1>surf browser</h1>
<p>Typ een adres in de balk hierboven en druk op Enter.
Scroll met het wiel, klik links om te volgen.</p>
<hr>
<p><b>x/net/html</b> parset de pagina's; deze layout-engine zet de DOM om in
pixels. Geen scripts &mdash; wel CSS, en vooral: <i>leesbaar</i>.</p>
<ul><li>blokken en woordwrap</li><li>koppen op schaal</li>
<li>links: <a href="about:blank">klikbaar</a></li>
<li><code>code</code> en <pre>  pre met  spaties</pre></li></ul>
</body></html>`

const userAgent = "surf/0.1 (HopOS)" // Wikipedia c.s. weigeren anonieme clients (403)

// pageMaxBytes begrenst één HTML-document over de lijn — zelfde gedachte
// als bij afbeeldingen en stylesheets: bare metal, begrensde heap.
const pageMaxBytes = 8 << 20

// Session is één browservenster.
type Session struct {
	client  *http.Client
	doc     *html.Node
	addr    *url.URL               // adres van de huidige pagina (na redirects)
	history []string               // verlaten pagina's, oudste eerst — de terug-knop
	base    *url.URL               // addr + <base href>: anker voor relatieve links
	imgs    map[string]image.Image // gedecodeerde <img>'s van de huidige pagina, op raw src
	edits   map[*html.Node]string  // ingetikte veldwaarden (overleven een re-layout)
	icon    image.Image            // apple-touch-icon: vult het logo-slot als de site zelf svg/JS is

	// De cascade in twee stappen: matchen is duur en breedte-onafhankelijk
	// (één keer per pagina, in de nav-goroutine), de media-evaluatie is
	// goedkoop en gebeurt per layout-breedte — zo schakelt een resize
	// tussen de mobiele en de desktop-versie van de site.
	matched    []matchedRule
	styleCache map[*html.Node]props
	styleW     int
}

// matchedRule is één gematchte CSS-regel: zijn declaraties, de elementen
// die hij raakt en de media-condities waaronder hij geldt. De volgorde in
// Session.matched ís de cascade-volgorde (specificiteit, dan bron).
type matchedRule struct {
	mq    []string
	decls props
	nodes []*html.Node
}

// NewSession start een venster op de ingebouwde startpagina, met het echte
// netwerk erachter (timeout + cookie-jar + CA-bundel, zie netClient).
func NewSession() *Session { return newSession(netClient) }

// NewSessionHandler is NewSession met een in-process http.Handler in plaats
// van het echte netwerk — voor de host-tests, zonder poorten of sockets.
func NewSessionHandler(h http.Handler) *Session {
	return newSession(&http.Client{Transport: handlerTransport{h}, Jar: newJar(), Timeout: 20 * time.Second})
}

func newSession(c *http.Client) *Session {
	s := &Session{client: c}
	s.doc, _ = html.Parse(strings.NewReader(welkomHTML))
	s.addr, _ = url.Parse("about:blank")
	s.base = s.addr
	return s
}

// cacert.pem is Mozilla's root-CA-bundel (via https://curl.se/ca/cacert.pem,
// MPL-2.0) — tamago heeft geen certificaatwinkel, dus zonder deze bundel
// faalt élke HTTPS-handshake op het device met "x509: certificate signed by
// unknown authority". Ververs hem af en toe met:
//
//	curl -fsSL https://curl.se/ca/cacert.pem -o app/browse/cacert.pem
//
//go:embed cacert.pem
var cacertPEM []byte

// netClient met een cookie-jar: consent-muren (DPG's privacy-gate op
// tweakers.net en nu.nl) zetten hun "akkoord"-cookie op een redirect — zonder
// jar kom je eeuwig op de muur terug. Eén jar per proces: dit is een
// éénpersoonsbrowser. De timeout is het netbeleid: een dooie site mag de
// nav-goroutine nooit eeuwig vasthouden.
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

// handlerTransport laat de client tegen een http.Handler praten in plaats
// van het net — de tests draaien zo de hele keten (redirects, cookies,
// subresources) zonder sockets. Bewust geen httptest: dit bestand linkt
// mee in de app.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := &recorder{hdr: http.Header{}, code: http.StatusOK}
	t.h.ServeHTTP(rec, req)
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", rec.code, http.StatusText(rec.code)),
		StatusCode:    rec.code,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        rec.hdr,
		Body:          io.NopCloser(bytes.NewReader(rec.buf.Bytes())),
		ContentLength: int64(rec.buf.Len()),
		Request:       req,
	}, nil
}

// recorder is de minimale http.ResponseWriter voor handlerTransport.
type recorder struct {
	hdr  http.Header
	code int
	buf  bytes.Buffer
}

func (r *recorder) Header() http.Header         { return r.hdr }
func (r *recorder) WriteHeader(c int)           { r.code = c }
func (r *recorder) Write(b []byte) (int, error) { return r.buf.Write(b) }

// --- navigatie ---------------------------------------------------------------

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

// Follow navigeert naar een aangeklikte href; relatieve paden ("/x",
// "page2.html", "#anker") resolven tegen de huidige pagina (incl. <base>).
func (s *Session) Follow(href string) error {
	return s.navigate(href)
}

// Back navigeert naar de laatst verlaten pagina (de terug-knop). Een
// mislukte terug-hop laat de historie intact; zonder historie is het een
// nette fout voor de statusbalk.
func (s *Session) Back() error {
	if len(s.history) == 0 {
		return fmt.Errorf("geen vorige pagina")
	}
	prev := s.history[len(s.history)-1]
	if err := s.navigate(prev); err != nil {
		return err
	}
	// navigate pushte de zojuist verlaten pagina: die én de bestemming
	// zelf van de stapel — terug is geen vooruit.
	s.history = s.history[:max(len(s.history)-2, 0)]
	return nil
}

// navigate is de gedeelde landing van Go en Follow: resolven, laden, een
// eventuele consent-muur één keer door, en dan de subresources laden.
func (s *Session) navigate(ref string) error {
	prev := s.URL() // voor de historie: de pagina die we (mogelijk) verlaten
	u, err := s.resolve(ref)
	if err != nil {
		return err
	}
	// Alleen-een-anker: geen nieuwe pagina — laat de huidige staan.
	if u.Fragment != "" && sameDoc(u, s.addr) {
		return nil
	}
	doc, final, err := s.load(u)
	if err != nil {
		return err
	}
	// Consent-muur (DPG's privacy-gate: tweakers.net, nu.nl, ...)? Zonder
	// scripts is die pagina letterlijk leeg — de door-URL staat er wel
	// gewoon in. Die éne hop zet het consent-cookie (jar!); daarna opnieuw
	// naar het gevraagde adres, zodat de adresbalk niet de redirect-junk
	// (?referrer=...) toont. Faalt de hop, dan blijft de muur staan en zie
	// je tenminste wáár je bent. Eén hop, dus nooit een lus.
	if gate := consentGateURL(doc); gate != "" {
		if gu, perr := url.Parse(gate); perr == nil {
			if _, _, gerr := s.load(gu); gerr == nil {
				if doc2, final2, err2 := s.load(u); err2 == nil {
					doc, final = doc2, final2
				}
			}
		}
	}
	// DPG's redirects plakken een ?referrer=... aan het adres (ook zónder
	// gate, als het consent-cookie er al zit): analytics-junk, geen inhoud
	// — niet in de balk en niet als base voor links.
	if q := final.Query(); q.Has("referrer") {
		q.Del("referrer")
		clean := *final
		clean.RawQuery = q.Encode()
		final = &clean
	}
	s.doc, s.addr = doc, final
	// Historie voor de terug-knop: de pagina die we zojuist verlieten
	// (herladen van hetzelfde adres telt niet, de lege start ook niet).
	if prev != "" && prev != "about:blank" && prev != final.String() {
		s.history = append(s.history, prev)
		if len(s.history) > 32 {
			s.history = s.history[1:]
		}
	}
	s.base = pageBase(doc, final)
	s.edits = nil   // nieuwe pagina, verse velden
	s.resolveUses() // svg <use> → symbolen inlijmen (sprite-sheets ophalen)
	s.loadStyles()
	s.loadImages()
	s.loadIcon()
	return nil
}

// resolve maakt van een adres of href een absolute http(s)-URL. Spaties
// ín het pad ("waarom 1.webp" — easyflorist) encoderen we zoals elke
// browser dat doet; url.Parse zou er anders op stuklopen.
func (s *Session) resolve(ref string) (*url.URL, error) {
	r, err := url.Parse(strings.ReplaceAll(strings.TrimSpace(ref), " ", "%20"))
	if err != nil {
		return nil, err
	}
	if s.base != nil {
		r = s.base.ResolveReference(r)
	}
	if r.Scheme != "http" && r.Scheme != "https" {
		return nil, fmt.Errorf("geen webadres: %s", ref)
	}
	return r, nil
}

func sameDoc(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	ac, bc := *a, *b
	ac.Fragment, bc.Fragment = "", ""
	return ac.String() == bc.String()
}

// load haalt één pagina op en parset hem; de fout is de échte fout van de
// lijn ("no such host", "x509: …", "HTTP 404") — geen platgeslagen
// tussenlaag meer. final is de URL ná redirects: dat is waar je bént.
func (s *Session) load(u *url.URL) (doc *html.Node, final *url.URL, err error) {
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, nil, fmt.Errorf("HTTP %s  %s", resp.Status, u)
	}
	body := io.LimitReader(resp.Body, pageMaxBytes)
	r, err := charset.NewReader(body, resp.Header.Get("Content-Type"))
	if err != nil {
		r = body // onbekende charset: rauw parsen is beter dan niets
	}
	doc, err = html.Parse(r)
	if err != nil {
		return nil, nil, err
	}
	final = u
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL
	}
	return doc, final, nil
}

// get haalt één subresource op (stylesheet, afbeelding, icoon).
func (s *Session) get(u string) (*http.Response, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	return s.client.Do(req)
}

// pageBase: de basis voor relatieve links — de paginalocatie, tenzij een
// <base href> anders zegt.
func pageBase(doc *html.Node, final *url.URL) *url.URL {
	if b := findEl(doc, "base"); b != nil {
		if href, ok := attr(b, "href"); ok && strings.TrimSpace(href) != "" {
			if r, err := url.Parse(strings.TrimSpace(href)); err == nil {
				return final.ResolveReference(r)
			}
		}
	}
	return final
}

// consentGateURL herkent een consent-muur en geeft de "klik hier verder"-URL
// terug ("" als de pagina geen muur is). Het patroon (DPG Media, het halve
// NL-web): een <script> met decodeURIComponent('https%3A%2F%2F...') waarin
// een privacy-bevestigings-URL met authId zit; die URL GET'en — met de
// cookie-jar — telt als de minimale (functionele-cookies) doorklik.
func consentGateURL(doc *html.Node) string {
	const marker = "decodeURIComponent('"
	found := ""
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if found != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "script" {
			txt := textContent(n)
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
		for c := n.FirstChild; c != nil; c = c.NextSibling {
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
		s.edits = map[*html.Node]string{}
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
	if m, _ := attr(form, "method"); strings.EqualFold(strings.TrimSpace(m), "post") {
		return fmt.Errorf("POST-formulier: nog niet gedragen")
	}
	q := url.Values{}
	var collect func(n *html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			name, _ := attr(n, "name")
			typ, _ := attr(n, "type")
			typ = strings.ToLower(typ)
			val, _ := attr(n, "value")
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
				if _, checked := attr(n, "checked"); checked {
					q.Set(name, val)
				}
			default: // text, search, hidden, ...
				q.Set(name, val)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	for c := form.FirstChild; c != nil; c = c.NextSibling {
		collect(c)
	}
	action, _ := attr(form, "action")
	if action == "" {
		action = s.URL()
	}
	if i := strings.IndexByte(action, '?'); i >= 0 {
		action = action[:i]
	}
	return s.Follow(action + "?" + q.Encode())
}

// ancestorForm zoekt het omvattende <form>-element.
func ancestorForm(n *html.Node) *html.Node {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && p.Data == "form" {
			return p
		}
	}
	return nil
}

// --- CSS laden en matchen -----------------------------------------------------

// Grenzen (zelfde gedachte als bij de afbeeldingen: bare metal, begrensde
// heap en begrensde tijd).
const (
	cssMaxSheets  = 12        // externe stylesheets per pagina (tweakers: 11, de header-regel zit in #8)
	cssMaxImports = 8         // @import-sheets bovenop de links (elke import is een fetch)
	cssMaxBytes   = 256 << 10 // per sheet, over de lijn
	cssMaxRules  = 10240     // na filtering; media reist mee, dus mobiel+desktop samen — krap cappen kost juist de mobiele overrides
	cssBudget    = 5 * time.Second
)

// loadStyles verzamelt de <style>-blokken en <link rel=stylesheet>-sheets,
// parset ze tot regels en matcht elke regel één keer met cascadia over de
// boom, in cascade-volgorde (specificiteit, bron). Het resultaat is
// Session.matched — breedte-onafhankelijk; stylesFor rekent daar per
// framebreedte de computed props uit. Draait in de nav-goroutine.
func (s *Session) loadStyles() {
	s.matched, s.styleCache, s.styleW = nil, nil, 0
	var rules []cssRule
	links := 0
	// media=""-attribuut van de sheet: reist als conditie met de regels mee,
	// net als een omhullend @media-blok.
	sheetMQ := func(n *html.Node) ([]string, bool) {
		m, ok := attr(n, "media")
		if !ok || strings.TrimSpace(m) == "" {
			return nil, true
		}
		if !mediaAnyWidth(m) {
			return nil, false // print e.d.: kan nooit gelden
		}
		return []string{m}, true
	}
	// Eerst verzamelen (de cascade-volgorde is heilig), dan de externe
	// sheets parallel over de lijn, dan in bronvolgorde parsen.
	type bron struct {
		tekst string
		href  string
		mq    []string
	}
	var bronnen []*bron
	eachEl(s.doc, func(n *html.Node) {
		switch n.Data {
		case "style":
			if mq, ok := sheetMQ(n); ok {
				bronnen = append(bronnen, &bron{tekst: textContent(n), mq: mq})
			}
		case "link":
			rel, _ := attr(n, "rel")
			href, _ := attr(n, "href")
			if strings.EqualFold(strings.TrimSpace(rel), "stylesheet") && href != "" && links < cssMaxSheets {
				if mq, ok := sheetMQ(n); ok {
					links++
					bronnen = append(bronnen, &bron{href: href, mq: mq})
				}
			}
		}
	})
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, b := range bronnen {
		if b.href == "" {
			continue
		}
		wg.Add(1)
		go func(b *bron) {
			defer wg.Done()
			sem <- struct{}{}
			b.tekst = s.fetchText(b.href)
			<-sem
		}(b)
	}
	wg.Wait()
	importBudget := cssMaxImports
	for _, b := range bronnen {
		// @import: sheets die sheets laden — relatieve verwijzingen resolven
		// tegen de importerende sheet, niet tegen de pagina.
		base := s.base
		if b.href != "" {
			if u, err := s.resolve(b.href); err == nil {
				base = u
			}
		}
		b.tekst = s.expandImports(b.tekst, base, &importBudget, 0)
		rules = append(rules, parseCSSm(b.tekst, len(rules), b.mq)...)
	}
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
	deadline := time.Now().Add(cssBudget)
	for _, r := range rules {
		if time.Now().After(deadline) {
			break // liever een half gestylede pagina dan een hangende browser
		}
		sel, err := cascadia.Parse(r.sel)
		if err != nil {
			continue // selector die cascadia niet kent: regel vervalt
		}
		nodes := cascadia.QueryAll(s.doc, sel)
		if len(nodes) == 0 {
			continue
		}
		s.matched = append(s.matched, matchedRule{mq: r.mq, decls: r.decls, nodes: nodes})
	}
}

// stylesFor rekent de computed props uit voor deze framebreedte: de
// gematchte regels langs (cascade-volgorde), media-condities evalueren,
// var()'s oplossen. Goedkoop genoeg om per resize te doen — zo IS een
// breed venster de desktopsite. De laatste breedte is gecachet.
func (s *Session) stylesFor(width int) map[*html.Node]props {
	if s.styleCache != nil && s.styleW == width {
		return s.styleCache
	}
	styles := map[*html.Node]props{}
	// Presentational hints: het width/height-attribuut van svg's en
	// ouderwetse tabellen is per spec een declaratie op de láágste plek in
	// de cascade — elke echte CSS-regel wint er dus van (ze staan hier vóór
	// de matched-lus). <img> houdt bewust zijn eigen attribuut-pad in
	// imgSize: daar hoort de beeldverhouding-regel bij (CSS height:auto
	// schakelt het attribuut uit — wikipedia's ei).
	eachEl(s.doc, func(n *html.Node) {
		switch n.Data {
		case "svg", "td", "th", "table":
			for _, k := range []string{"width", "height"} {
				if v, ok := attr(n, k); ok {
					if hv := hintLen(v); hv != "" {
						p := styles[n]
						if p == nil {
							p = props{}
							styles[n] = p
						}
						p[k] = hv
					}
				}
			}
		}
	})
	vars := map[string]string{} // custom properties, doc-globaal (versimpeld: geen scoping)
	for _, r := range s.matched {
		if !ruleMediaOK(r.mq, width) {
			continue
		}
		for _, n := range r.nodes {
			p := styles[n]
			if p == nil {
				p = props{}
				styles[n] = p
			}
			for k, v := range r.decls {
				p[k] = v
			}
		}
		// --vars van geldende regels (:root, body, body.pg-x) gelden
		// doc-globaal in cascade-volgorde — genoeg voor het gangbare
		// "thema op de body"-patroon.
		for k, v := range r.decls {
			if strings.HasPrefix(k, "--") {
				vars[k] = v
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
	if root := findEl(s.doc, "html"); root != nil {
		if hp := styles[root]; hp != nil {
			if body := findEl(s.doc, "body"); body != nil {
				bp := styles[body]
				if bp == nil {
					bp = props{}
					styles[body] = bp
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
	s.styleCache, s.styleW = styles, width
	return styles
}

// expandImports vervangt @import-statements door de inhoud van de
// geïmporteerde sheet — zonder dit bestaan sheets die zo bundelen
// simpelweg niet en blijft de pagina half ongestyled. Een mediaconditie
// op de import wordt een omhullend @media-blok (dezelfde evaluatie als
// elke andere query), een supports(...)-conditie evalueert tegen
// supportedProp. budget en depth begrenzen het fetchen (import-cycli!).
func (s *Session) expandImports(css string, base *url.URL, budget *int, depth int) string {
	if depth >= 3 || !strings.Contains(css, "@import") {
		return css
	}
	css = stripComments(css)
	var out strings.Builder
	for i := 0; i < len(css); {
		j := strings.Index(css[i:], "@import")
		if j < 0 {
			out.WriteString(css[i:])
			break
		}
		j += i
		end := strings.IndexByte(css[j:], ';')
		if end < 0 {
			out.WriteString(css[i:])
			break
		}
		out.WriteString(css[i:j])
		stmt := css[j+len("@import") : j+end]
		i = j + end + 1
		ref, mq, ok := importTarget(stmt)
		if !ok || ref == "" || strings.HasPrefix(ref, "data:") || *budget <= 0 || base == nil {
			continue
		}
		if mq != "" && !mediaAnyWidth(mq) {
			continue // print e.d.: kan op geen enkele breedte gelden
		}
		u, err := base.Parse(ref)
		if err != nil {
			continue
		}
		*budget--
		sub := s.expandImports(s.fetchText(u.String()), u, budget, depth+1)
		if mq != "" {
			sub = "@media " + mq + "{" + sub + "}"
		}
		out.WriteString(sub)
	}
	return out.String()
}

// importTarget leest het doel uit een @import-statement: url(...) of een
// string, daarna optioneel layer(...)/layer en supports(...) — de rest is
// de mediaquery. ok=false als een supports-conditie faalt.
func importTarget(stmt string) (ref, mq string, ok bool) {
	stmt = strings.TrimSpace(stmt)
	switch {
	case strings.HasPrefix(strings.ToLower(stmt), "url("):
		end := closeParen(stmt, 3)
		if end < 0 {
			return "", "", false
		}
		ref = strings.Trim(strings.TrimSpace(stmt[4:end]), `"'`)
		stmt = stmt[end+1:]
	case len(stmt) > 1 && (stmt[0] == '"' || stmt[0] == '\''):
		j := strings.IndexByte(stmt[1:], stmt[0])
		if j < 0 {
			return "", "", false
		}
		ref = stmt[1 : 1+j]
		stmt = stmt[j+2:]
	default:
		return "", "", false
	}
	rest := strings.TrimSpace(stmt)
	for {
		low := strings.ToLower(rest)
		switch {
		case strings.HasPrefix(low, "layer("):
			end := closeParen(rest, len("layer(")-1)
			if end < 0 {
				return ref, "", true
			}
			rest = strings.TrimSpace(rest[end+1:])
		case strings.HasPrefix(low, "layer"):
			rest = strings.TrimSpace(rest[len("layer"):])
		case strings.HasPrefix(low, "supports("):
			end := closeParen(rest, len("supports(")-1)
			if end < 0 {
				return ref, "", true
			}
			if !supportsCond(rest[len("supports("):end]) {
				return "", "", false // conditie faalt: de import vervalt
			}
			rest = strings.TrimSpace(rest[end+1:])
		default:
			return ref, rest, true
		}
	}
}

// fetchText haalt één tekst-subresource (stylesheet) begrensd op; "" bij pech.
func (s *Session) fetchText(href string) string {
	u, err := s.resolve(href)
	if err != nil {
		return ""
	}
	resp, err := s.get(u.String())
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

// --- afbeeldingen -------------------------------------------------------------

// Grenzen voor het afbeeldingen laden: dit draait straks op bare metal, en
// een pagina vol foto's mag de heap niet opblazen. Boven de kaders → alt-
// tekst, net als bij een laadfout.
const (
	imgMaxCount = 32      // per pagina
	imgMaxBytes = 8 << 20 // per afbeelding, over de lijn (easyflorist: 4,8MB-webp's)
	imgMaxDim   = 2048    // px, per zijde — wat we bewáren (2048² RGBA = 16MB)
	// De decode-piek die we aandurven: sites serveren rustig 24-megapixel
	// foto's (easyflorist: 6000×4000 webp). jpeg/webp decoderen naar YCbCr
	// (~2 B/px), png/gif naar RGBA (4 B/px) — na de decode schalen we
	// meteen terug naar imgMaxDim, dus dit is een píek, geen bezit.
	imgMaxDecode = 96 << 20 // bytes (easyflorists grootste: 7952×5304 webp ≈ 84MB)
)

// loadImages haalt de <img src>'s van de huidige pagina op en decodeert ze,
// gesleuteld op het rauwe src-attribuut (waar de layout ze op terugvindt).
// Fouten zijn per afbeelding en stil: de layout valt terug op de alt-tekst.
// Draait in de nav-goroutine — de event-lus merkt er niets van.
func (s *Session) loadImages() {
	s.imgs = nil
	seen := map[string]bool{}
	var srcs []string
	load := func(src string) {
		if src == "" || seen[src] || len(seen) >= imgMaxCount {
			return
		}
		seen[src] = true
		srcs = append(srcs, src)
	}
	eachEl(findEl(s.doc, "body"), func(n *html.Node) {
		if n.Data == "img" {
			// Dezelfde bron-keuze als de layout (src/data-src/srcset).
			load(imgSrc(n))
		}
		if n.Data == "video" {
			// De poster is het beeld dat de layout toont.
			if v, ok := attr(n, "poster"); ok {
				load(v)
			}
		}
		// background-image uit een inline style — de layout zoekt hem
		// straks op dezelfde sleutel (de rauwe url) terug.
		if inline, ok := attr(n, "style"); ok {
			if v, ok := parseDecls(inline)["background-image"]; ok {
				load(cssURL(v))
			}
		}
	})
	// background-images uit de stylesheets: uit álle gematchte regels —
	// niet alleen die van de mobiele breedte, anders mist de desktop-
	// layout straks zijn achtergronden.
	for _, r := range s.matched {
		if v, ok := r.decls["background-image"]; ok {
			load(cssURL(v))
		}
	}
	// En dan alles tegelijk over de lijn: het wachten zit in het netwerk,
	// niet in de CPU — zes verbindingen naast elkaar halen een fotorijke
	// pagina in een fractie van de seriële tijd binnen.
	type gehaald struct {
		src string
		m   image.Image
	}
	out := make(chan gehaald)
	sem := make(chan struct{}, 6)
	for _, src := range srcs {
		go func(src string) {
			sem <- struct{}{}
			m := s.fetchImage(src)
			<-sem
			out <- gehaald{src, m}
		}(src)
	}
	for range srcs {
		g := <-out
		if g.m != nil {
			if s.imgs == nil {
				s.imgs = map[string]image.Image{}
			}
			s.imgs[g.src] = g.m
		}
	}
}

// fetchImage haalt één afbeelding op (src opgelost tegen de pagina) en
// decodeert hem; nil bij elke vorm van pech of buiten de kaders.
func (s *Session) fetchImage(src string) image.Image {
	u, err := s.resolve(src)
	if err != nil {
		return nil
	}
	resp, err := s.get(u.String())
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
	// SVG (logo's, iconen): rasteren op eigen maat — daarna is het gewoon
	// een afbeelding als elke andere. Sprite-vellen met geneste <svg id>'s
	// eerst: die zou oksvg tot één klodder plakken.
	if looksLikeSVG(resp.Header.Get("Content-Type"), data) {
		if m := rasterSVGSheet(data, imgMaxDim); m != nil {
			return m
		}
		if m := rasterSVGNatural(data, 1024); m != nil {
			return m
		}
		return nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 {
		return nil
	}
	perPix := 4 // png/gif: RGBA-achtig
	if format == "jpeg" || format == "webp" {
		perPix = 2 // YCbCr 4:2:0 ≈ 1,5 B/px, met marge
	}
	if cfg.Width*cfg.Height*perPix > imgMaxDecode {
		return nil // écht te groot: dan liever de alt-tekst
	}
	m, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	// Reuzefoto's meteen terugschalen: de decode-piek is tijdelijk, wat we
	// bewaren blijft binnen het kader (≤2048 per zijde) — meer dan zat
	// voor het scherm, en 32 foto's op een pagina blijven zo betaalbaar.
	if b := m.Bounds(); b.Dx() > imgMaxDim || b.Dy() > imgMaxDim {
		w, h := b.Dx(), b.Dy()
		if w > imgMaxDim {
			h, w = h*imgMaxDim/w, imgMaxDim
		}
		if h > imgMaxDim {
			w, h = w*imgMaxDim/h, imgMaxDim
		}
		if w < 1 || h < 1 {
			return nil
		}
		m = scaleTo(m, w, h)
	}
	return m
}

// --- API voor main ------------------------------------------------------------

// URL is het adres van de huidige pagina (voor de adresbalk na navigatie).
func (s *Session) URL() string {
	if s.addr == nil {
		return ""
	}
	return s.addr.String()
}

// Layout layout de huidige pagina voor deze breedte, inclusief de bij de
// navigatie opgehaalde afbeeldingen, CSS-props en ingetikte veldwaarden.
func (s *Session) Layout(width int) Page {
	return layoutStyled(findEl(s.doc, "body"), width, s.imgs, s.stylesFor(width), s.edits, s.icon)
}

// loadIcon haalt het site-icoon op (apple-touch-icon, of een png-icon, of
// het well-known pad): de vulling voor het logo-slot — sites tekenen hun
// logo met svg of een webcomponent, en dit icoon is hun eigen officiële
// vervanger (het homescreen-plaatje).
func (s *Session) loadIcon() {
	s.icon = nil
	if s.addr == nil || (s.addr.Scheme != "http" && s.addr.Scheme != "https") {
		return
	}
	href := ""
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "link" {
			rel, _ := attr(n, "rel")
			rel = strings.ToLower(rel)
			h, ok := attr(n, "href")
			if ok && h != "" {
				if strings.Contains(rel, "apple-touch-icon") {
					href = h // het homescreen-icoon: altijd de beste
				} else if href == "" && strings.Contains(rel, "icon") &&
					strings.Contains(strings.ToLower(h), ".png") {
					href = h
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(s.doc)
	if href == "" {
		href = "/apple-touch-icon.png" // well-known pad: tweakers heeft geen link-tag, wél het icoon
	}
	s.icon = s.fetchImage(href)
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
