// Session is de koppeling met gost-dom: één browservenster dat pagina's
// ophaalt en parset. Scripting staat bewust uit (gost-dom kan Goja/V8
// aanhaken, maar V8 is cgo en Goja een flinke dep — statische pagina's
// eerst). gost-dom lost zelf relatieve hrefs en de history op; wij hoeven
// alleen Navigate te roepen met wat de gebruiker aanklikt of intikt.
package browse

import (
	"bytes"
	"image"
	_ "image/gif" // decoders voor <img>: puur Go, dus ook op tamago
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
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
	b    *gost.Browser
	win  html.Window
	imgs map[string]image.Image // gedecodeerde <img>'s van de huidige pagina, op raw src
}

// NewSession start een venster op de ingebouwde startpagina. Het netwerk
// gaat via netProxy: gost-dom's eigen fetch heeft géén timeout, en één dooie
// site zou anders de hele event-lus voorgoed bevriezen ("hij staat vast",
// Derek 19-07 — de browser hing op een niet-antwoordende host).
func NewSession() *Session {
	return newSession(gost.New(gost.WithHandler(netProxy{}), gost.WithScriptEngine(nopEngine{})))
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

func (nopEngine) NewHost(html.ScriptEngineOptions) html.ScriptHost   { return nopHost{} }
func (nopHost) NewContext(html.BrowsingContext) html.ScriptContext   { return nopContext{} }
func (nopHost) Close()                                               {}
func (nopContext) Eval(string) (any, error)                          { return nil, nil }
func (nopContext) Run(string) error                                  { return nil }
func (nopContext) Compile(string) (html.Script, error)               { return nopScript{}, nil }
func (nopContext) DownloadScript(string) (html.Script, error)        { return nopScript{}, nil }
func (nopContext) DownloadModule(string) (html.Script, error)        { return nopScript{}, nil }
func (nopContext) Close()                                            {}
func (nopScript) Eval() (any, error)                                 { return nil, nil }
func (nopScript) Run() error                                         { return nil }

// netProxy is het "netwerk" voor gost-dom: een http.Handler die de request
// écht uitvoert, met een harde timeout. Zo heeft de hele browser één plek
// voor netbeleid (straks ook: alleen-origin voor gast-apps, §10-vergezicht).
type netProxy struct{}

var netClient = &http.Client{Timeout: 20 * time.Second}

func (netProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// De inkomende (server-vormige) request omzetten naar een uitgaande.
	out, err := http.NewRequest(r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	out.Header = r.Header.Clone()
	if out.Header.Get("User-Agent") == "" {
		// Wikipedia c.s. weigeren anonieme clients (403); wees gewoon wie
		// we zijn.
		out.Header.Set("User-Agent", "surf/0.1 (HopOS; gost-dom)")
	}
	resp, err := netClient.Do(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
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
	err := s.win.Navigate(addr)
	if err == nil {
		s.loadImages()
	}
	return err
}

// Follow navigeert naar een aangeklikte href, onaangeroerd: gost-dom lost
// relatieve paden ("/x", "page2.html", "#anker") tegen de huidige pagina op.
func (s *Session) Follow(href string) error {
	err := s.win.Navigate(href)
	if err == nil {
		s.loadImages()
	}
	return err
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
	var walk func(n dom.Node)
	walk = func(n dom.Node) {
		if len(seen) >= imgMaxCount {
			return
		}
		if el, ok := n.(dom.Element); ok && strings.EqualFold(el.TagName(), "img") {
			if src, _ := el.GetAttribute("src"); src != "" && !seen[src] {
				seen[src] = true
				if m := s.fetchImage(base, src); m != nil {
					if s.imgs == nil {
						s.imgs = map[string]image.Image{}
					}
					s.imgs[src] = m
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
// navigatie opgehaalde afbeeldingen.
func (s *Session) Layout(width int) Page {
	return LayoutWithImages(s.win.Document().Body(), width, s.imgs)
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
