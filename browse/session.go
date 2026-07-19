// Session is de koppeling met gost-dom: één browservenster dat pagina's
// ophaalt en parset. Scripting staat bewust uit (gost-dom kan Goja/V8
// aanhaken, maar V8 is cgo en Goja een flinke dep — statische pagina's
// eerst). gost-dom lost zelf relatieve hrefs en de history op; wij hoeven
// alleen Navigate te roepen met wat de gebruiker aanklikt of intikt.
package browse

import (
	"io"
	"net/http"
	"time"

	gost "github.com/gost-dom/browser"
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
	b   *gost.Browser
	win html.Window
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
	return s.win.Navigate(addr)
}

// Follow navigeert naar een aangeklikte href, onaangeroerd: gost-dom lost
// relatieve paden ("/x", "page2.html", "#anker") tegen de huidige pagina op.
func (s *Session) Follow(href string) error {
	return s.win.Navigate(href)
}

// URL is het adres van de huidige pagina (voor de adresbalk na navigatie).
func (s *Session) URL() string {
	return s.win.Location().Href()
}

// Layout layout de huidige pagina voor deze breedte.
func (s *Session) Layout(width int) Page {
	return Layout(s.win.Document().Body(), width)
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
