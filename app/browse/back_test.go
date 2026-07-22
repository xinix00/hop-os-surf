package browse

import (
	"net/http"
	"testing"
)

// TestBack: de terug-knop — Follow bouwt historie op, Back loopt hem terug
// (en is geen vooruit: de stapel krimpt), en zonder historie is het een
// nette fout voor de statusbalk.
func TestBack(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><h1>A</h1><a href="/b">naar b</a></body></html>`))
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><h1>B</h1></body></html>`))
	})
	s := NewSessionHandler(mux)

	if err := s.Back(); err == nil {
		t.Fatal("terug zonder historie hoort een nette fout te geven")
	}
	if err := s.Go("http://site.test/a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Follow("/b"); err != nil {
		t.Fatal(err)
	}
	if got := s.URL(); got != "http://site.test/b" {
		t.Fatalf("op /b horen te staan: %s", got)
	}

	if err := s.Back(); err != nil {
		t.Fatal(err)
	}
	if got := s.URL(); got != "http://site.test/a" {
		t.Fatalf("terug hoort /a te zijn: %s", got)
	}
	if err := s.Back(); err == nil {
		t.Fatal("na één terug hoort de historie leeg te zijn (terug is geen vooruit)")
	}
}
