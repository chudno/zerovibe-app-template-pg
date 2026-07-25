package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func serve(m *Mux, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestMethodPatterns(t *testing.T) {
	m := New()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	m.HandleFunc("POST /notes", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) })

	if c := serve(m, "GET", "/healthz").Code; c != 204 {
		t.Fatalf("GET /healthz → %d", c)
	}
	if c := serve(m, "POST", "/notes").Code; c != 201 {
		t.Fatalf("POST /notes → %d", c)
	}
	// Тот же путь, чужой метод → 405 с Allow.
	rec := serve(m, "DELETE", "/notes")
	if rec.Code != 405 || rec.Header().Get("Allow") == "" {
		t.Fatalf("DELETE /notes → %d, Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
	if c := serve(m, "GET", "/nope").Code; c != 404 {
		t.Fatalf("GET /nope → %d", c)
	}
}

func TestPathParams(t *testing.T) {
	m := New()
	var got string
	m.HandleFunc("POST /events/{id}/signup", func(w http.ResponseWriter, r *http.Request) {
		got = r.PathValue("id")
		w.WriteHeader(200)
	})
	if c := serve(m, "POST", "/events/abc123/signup").Code; c != 200 || got != "abc123" {
		t.Fatalf("код=%d id=%q", c, got)
	}
	if c := serve(m, "POST", "/events/abc123").Code; c != 404 {
		t.Fatalf("неполный путь должен 404, получил %d", c)
	}
}

func TestSpecificityAndSubtree(t *testing.T) {
	m := New()
	m.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })          // всё
	m.HandleFunc("GET /static/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(206) })   // подветка
	m.HandleFunc("GET /notes/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(210) }) //nolint
	m.HandleFunc("GET /notes/new", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(211) })

	if c := serve(m, "GET", "/").Code; c != 200 {
		t.Fatalf("/ → %d", c)
	}
	if c := serve(m, "GET", "/anything").Code; c != 200 {
		t.Fatalf("подветка / должна ловить всё: %d", c)
	}
	if c := serve(m, "GET", "/static/app.css").Code; c != 206 {
		t.Fatalf("/static/app.css → %d", c)
	}
	if c := serve(m, "GET", "/notes/42").Code; c != 210 {
		t.Fatalf("/notes/42 → %d", c)
	}
	// Литерал важнее параметра при равной длине.
	if c := serve(m, "GET", "/notes/new").Code; c != 211 {
		t.Fatalf("/notes/new → %d (литерал должен победить {id})", c)
	}
}

func TestHandleWithoutMethod(t *testing.T) {
	m := New()
	m.HandleFunc("/any", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	for _, meth := range []string{"GET", "POST", "PUT"} {
		if c := serve(m, meth, "/any").Code; c != 200 {
			t.Fatalf("%s /any → %d", meth, c)
		}
	}
}
