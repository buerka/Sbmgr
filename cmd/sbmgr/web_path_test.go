package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestWebBasePathProtectsPageAssetsAndAPI(t *testing.T) {
	a, b := webFixture(t)
	c := b.config
	c.Origin, c.BasePath = "https://admin.example", "/fixture-entry"
	writeWebFixtureConfig(t, a, c)
	assets := fstest.MapFS{
		"index.html":        {Data: []byte(`<script src="./assets/fixture.js"></script>`)},
		"assets/fixture.js": {Data: []byte("fixture")},
	}
	server := newWebHTTPServerWithAssets("", c.Origin, c.BasePath, b.lookup, assets)
	call := func(method, path string, body []byte, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, c.Origin+path, bytes.NewReader(body))
		r.Header.Set("Origin", c.Origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		server.Handler.ServeHTTP(w, r)
		return w
	}
	for _, p := range []string{"/", "/api/login", "/api/state", "/assets/fixture.js", "/fixture-entry-extra/", "/fixture-entry//api/state", "/fixture-entry/../api/state", "/fixture-entry/assets/../fixture.js", "/fixture-entry%2fapi/state", "/%66ixture-entry/", "/fixture-entry/./api/state"} {
		for _, method := range []string{"GET", "POST"} {
			w := call(method, p, nil, nil, "")
			if w.Code != 404 || strings.Contains(w.Body.String(), c.BasePath) || w.Header().Get("Location") != "" {
				t.Fatalf("unrecognized %s path exposed an entry: status %d", method, w.Code)
			}
		}
	}
	if w := call("GET", c.BasePath, nil, nil, ""); w.Code != 308 || w.Header().Get("Location") != c.BasePath+"/" {
		t.Fatal("known entry requires a trailing slash for relative assets")
	}
	for _, p := range []string{c.BasePath + "/", c.BasePath + "/assets/fixture.js"} {
		w := call("GET", p, nil, nil, "")
		if w.Code != 200 || w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("prefixed assets unavailable or leaking references")
		}
	}
	if w := call("GET", c.BasePath+"/api/state", nil, nil, ""); w.Code != 401 {
		t.Fatal("path bypassed authentication")
	}
	data, _ := json.Marshal(map[string]string{"username": "admin", "password": webTestPassword})
	w := call("POST", c.BasePath+"/api/login", data, nil, "")
	cookies := w.Result().Cookies()
	if w.Code != 200 || len(cookies) != 1 || cookies[0].Path != c.BasePath+"/" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("prefixed login cookie invalid")
	}
	var login struct {
		CSRF string `json:"csrf"`
	}
	if json.Unmarshal(w.Body.Bytes(), &login) != nil {
		t.Fatal("invalid login reply")
	}
	if w := call("GET", c.BasePath+"/api/state", nil, cookies[0], ""); w.Code != 200 {
		t.Fatal("authenticated API unavailable")
	}
	if w := call("POST", c.BasePath+"/api/logout", []byte(`{}`), cookies[0], ""); w.Code != 403 {
		t.Fatal("path bypassed CSRF")
	}
	w = call("POST", c.BasePath+"/api/logout", []byte(`{}`), cookies[0], login.CSRF)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].Path != c.BasePath+"/" || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout did not clear scoped cookie")
	}
	if w := call("GET", c.BasePath+"/api/session", nil, cookies[0], ""); w.Code != 401 {
		t.Fatal("logout retained session")
	}
}

func TestWebBasePathBackendBoundaryAndRevocation(t *testing.T) {
	a, b := webFixture(t)
	q := loginWebFixture(t, b)
	c := b.config
	c.BasePath = "/changed-entry"
	writeWebFixtureConfig(t, a, c)
	q.Method, q.Path = "GET", "/api/session"
	if r := b.lookup(context.Background(), q); r.Status != 404 {
		t.Fatal("broker accepted an unprefixed request")
	}
	q.Path = c.BasePath + "/api/session"
	if r := b.lookup(context.Background(), q); r.Status != 401 {
		t.Fatal("base-path change retained a previous session")
	}
}

func TestWebBasePathConfigurationValidation(t *testing.T) {
	for _, base := range []string{"", "/test", "/A_b-123", "/" + strings.Repeat("x", 128)} {
		if err := validateWebBasePath(base); err != nil {
			t.Fatal("valid path rejected")
		}
	}
	for _, base := range []string{"/", "test", "/a/b", "/a/", "/../a", "/a?b", "/a#b", "/a%2fb", "/a\\b", "/<script>", "/a\n", "/" + strings.Repeat("x", 129)} {
		if validateWebBasePath(base) == nil {
			t.Fatal("ambiguous path accepted")
		}
	}
}
