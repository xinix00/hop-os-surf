package browse

import (
	"net/http"
	"strings"
	"testing"
)

// TestConsentGate: een DPG-achtige privacy-muur wordt één keer automatisch
// doorgeklikt (de door-URL uit het <script> gevist), waarna de sessie weer
// op het échte adres staat — adresbalk en relatieve links blijven kloppen.
func TestConsentGate(t *testing.T) {
	akkoord := false // staat serverzijde voor "consent gegeven"
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !akkoord {
			w.Write([]byte(`<html><head><script>
const callbackUrl = new URL(decodeURIComponent('http%3A%2F%2Fexample.com%2Fprivacy-gate%2Faccept%3FredirectUri%3D%252F%26authId%3Dabc-123'))
window._privacy = window._privacy || [];
</script></head><body><div style="visibility:hidden">muur</div></body></html>`))
			return
		}
		w.Write([]byte(`<html><body><h1>Echte voorpagina</h1></body></html>`))
	})
	mux.HandleFunc("/privacy-gate/accept", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("authId") != "abc-123" {
			http.Error(w, "geen authId", http.StatusBadRequest)
			return
		}
		akkoord = true
		http.Redirect(w, r, "/", http.StatusFound)
	})

	s := NewSessionHandler(mux)
	if err := s.Go("example.com"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	if got := s.URL(); !strings.HasSuffix(got, "example.com/") {
		t.Fatalf("na de muur niet terug op het echte adres: %q", got)
	}
	if find(s.Layout(480), "Echte voorpagina") == nil {
		t.Fatal("voorpagina achter de consent-muur niet geladen")
	}
}

// TestConsentGateURL: het patroon herkennen — en gewone pagina's met
// decodeURIComponent maar zónder privacy-URL met rust laten.
func TestConsentGateURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><script>
var x = decodeURIComponent('gewoon%20iets');
</script></head><body><p>geen muur</p></body></html>`))
	})
	s := NewSessionHandler(mux)
	if err := s.Go("example.com"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	if find(s.Layout(480), "geen muur") == nil {
		t.Fatal("pagina zonder muur hoort gewoon te renderen")
	}
}
