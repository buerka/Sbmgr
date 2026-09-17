package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"sbmgr/deploy"
)

const webTestPassword = "fixture-only-password-2026"

func webFixture(t *testing.T) (*app, *webBackend) {
	t.Helper()
	dir := t.TempDir()
	a := &app{statePath: filepath.Join(dir, "state.db"), out: io.Discard, err: io.Discard}
	s := sqliteFixtureState(1)
	s.BaseConfig, s.ConfigPath = filepath.Join(dir, "config.base.json"), filepath.Join(dir, "sing-box.json")
	s.Client.PublicKey = strings.Repeat("a", 43)
	s.Client.ServerName = "example.com"
	s.Subscription = SubscriptionSettings{Enabled: true, Listen: "127.0.0.1:18080", BaseURL: "https://sub.example"}
	if err := os.WriteFile(s.BaseConfig, []byte(sampleConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveState(a.statePath, s); err != nil {
		t.Fatal(err)
	}
	c := webConfig{Version: 1, Listen: "127.0.0.1:9090", Origin: "http://127.0.0.1:9090", Username: "admin", Salt: strings.Repeat("1", 64)}
	c.PasswordHash, _ = webPasswordHash(webTestPassword, c.Salt)
	writeWebFixtureConfig(t, a, c)
	return a, newWebBackend(a, c)
}

func writeWebFixtureConfig(t *testing.T, a *app, c webConfig) {
	t.Helper()
	data, _ := json.Marshal(c)
	if err := atomicWrite(webConfigPath(a.statePath), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func loginWebFixture(t *testing.T, b *webBackend) webRequest {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"username": "admin", "password": webTestPassword})
	q := webRequest{Method: "POST", Path: "/api/login", Host: "127.0.0.1:9090", Origin: "http://127.0.0.1:9090", Remote: "127.0.0.1", Body: data}
	r := b.lookup(context.Background(), q)
	if r.Status != 200 || r.SetSession == "" {
		t.Fatalf("login status %d", r.Status)
	}
	var session map[string]string
	if err := json.Unmarshal(r.Body, &session); err != nil {
		t.Fatal(err)
	}
	q.Session, q.CSRF, q.Body = r.SetSession, session["csrf"], nil
	return q
}

func TestWebAuthenticationOriginCSRFAndRevocation(t *testing.T) {
	a, b := webFixture(t)
	q := loginWebFixture(t, b)
	for _, tc := range []struct {
		name   string
		change func(*webRequest)
		want   int
	}{
		{"unauthenticated", func(q *webRequest) { q.Session = "" }, 401},
		{"host", func(q *webRequest) { q.Host = "attacker.example" }, 403},
		{"origin", func(q *webRequest) { q.Origin = "https://attacker.example" }, 403},
		{"csrf", func(q *webRequest) { q.CSRF = "" }, 403},
		{"missing origin", func(q *webRequest) { q.Origin = "" }, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := q
			bad.Path = "/api/actions"
			bad.Body = []byte(`{"action":"user.disable","fields":{"user":"alice"},"confirm":true}`)
			tc.change(&bad)
			if r := b.lookup(context.Background(), bad); r.Status != tc.want {
				t.Fatalf("status %d", r.Status)
			}
		})
	}
	q.Path, q.Method = "/api/session", "GET"
	if r := b.lookup(context.Background(), q); r.Status != 200 {
		t.Fatal("session missing")
	}
	b.sessions[q.Session] = webSession{q.CSRF, time.Now().Add(-time.Second)}
	if r := b.lookup(context.Background(), q); r.Status != 401 {
		t.Fatal("expired session accepted")
	}
	q = loginWebFixture(t, b)
	c := b.config
	c.PasswordHash, _ = webPasswordHash("replacement-password", c.Salt)
	writeWebFixtureConfig(t, a, c)
	q.Path, q.Method = "/api/session", "GET"
	if r := b.lookup(context.Background(), q); r.Status != 401 {
		t.Fatal("password change did not revoke session")
	}
}

func TestWebLoginBudgetAndStrictJSON(t *testing.T) {
	_, b := webFixture(t)
	q := webRequest{Method: "POST", Path: "/api/login", Host: "127.0.0.1:9090", Origin: "http://127.0.0.1:9090", Remote: "127.0.0.1", Body: []byte(`{"username":"admin","password":"wrong"}`)}
	for i := 0; i < 5; i++ {
		if r := b.lookup(context.Background(), q); r.Status != 401 {
			t.Fatalf("attempt %d status %d", i, r.Status)
		}
	}
	if r := b.lookup(context.Background(), q); r.Status != 429 {
		t.Fatal("login attempts unbounded")
	}
	for _, raw := range []string{`{"action":"x","extra":"bad"}`, `{} {}`, strings.Repeat("x", webMaxRequest+1)} {
		var input webActionInput
		if webDecode([]byte(raw), &input) == nil {
			t.Fatal("invalid input accepted")
		}
	}
}

func TestWebActionCatalogIsBoundedAndHasNoVersionManagement(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range webActions() {
		if seen[a.ID] || a.Fields == nil {
			t.Fatalf("invalid catalog action %s", a.ID)
		}
		seen[a.ID] = true
		for _, forbidden := range []string{"upgrade", "rollback", "version.history", "shell"} {
			if strings.Contains(a.ID, forbidden) {
				t.Fatal("runtime version or shell action")
			}
		}
	}
	for _, input := range []webActionInput{
		{Action: "shell"},
		{Action: "user.disable", Fields: map[string]string{"user": "alice"}},
		{Action: "user.set", Fields: map[string]string{"user": "alice", "uuid": "no"}},
		{Action: "user.set", Fields: map[string]string{"user": "--apply"}},
		{Action: "user.set", Fields: map[string]string{"user": "alice", "quota-mode": "invalid"}},
	} {
		if _, _, err := compileWebAction(input); err == nil {
			t.Fatalf("accepted invalid action %s", input.Action)
		}
	}
	args, _, err := compileWebAction(webActionInput{Action: "user.access", Fields: map[string]string{"user": "alice", "allow-domains": "-"}})
	if err != nil || !strings.Contains(strings.Join(args, "|"), "--allow-domains=") {
		t.Fatal("list clearing unavailable")
	}
}

func performWebAction(t *testing.T, b *webBackend, q webRequest, input webActionInput) webJob {
	t.Helper()
	q.Method, q.Path = "POST", "/api/actions"
	q.Body, _ = json.Marshal(input)
	r := b.lookup(context.Background(), q)
	if r.Status != 202 {
		t.Fatalf("submit %s status %d: %s", input.Action, r.Status, r.Body)
	}
	var job webJob
	_ = json.Unmarshal(r.Body, &job)
	q.Method, q.Path, q.Body = "GET", "/api/jobs/"+job.ID, nil
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r = b.lookup(context.Background(), q)
		_ = json.Unmarshal(r.Body, &job)
		if job.Status != "running" {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("action did not finish")
	return job
}

func TestWebMutationsAuditBatchAtomicityAndIdentityPreservation(t *testing.T) {
	a, b := webFixture(t)
	q := loginWebFixture(t, b)
	before, _ := loadState(a.statePath)
	job := performWebAction(t, b, q, webActionInput{Action: "user.set", Fields: map[string]string{"user": "alice", "quota": "40G"}})
	if job.Status != "success" {
		t.Fatal(job.Message)
	}
	after, _ := loadState(a.statePath)
	if after.Users[0].QuotaBytes != 40<<30 || after.Users[0].Nodes[0].UUID != before.Users[0].Nodes[0].UUID || after.Users[0].Devices[0].SubscriptionToken != before.Users[0].Devices[0].SubscriptionToken {
		t.Fatal("quota update changed identity or failed")
	}
	job = performWebAction(t, b, q, webActionInput{Action: "user.batch", Confirm: true, Fields: map[string]string{"users": "alice", "quota": "45"}})
	if job.Status != "success" {
		t.Fatal(job.Message)
	}
	after, _ = loadState(a.statePath)
	if after.Users[0].QuotaBytes != 45<<30 {
		t.Fatal("valid batch or bare GiB input failed")
	}
	job = performWebAction(t, b, q, webActionInput{Action: "user.batch", Confirm: true, Fields: map[string]string{"users": "alice,missing", "quota": "50G"}})
	if job.Status != "failed" {
		t.Fatal("invalid batch succeeded")
	}
	after, _ = loadState(a.statePath)
	if after.Users[0].QuotaBytes != 45<<30 {
		t.Fatal("failed batch partially committed")
	}
	job = performWebAction(t, b, q, webActionInput{Action: "user.access", Fields: map[string]string{"user": "alice", "block-ports": "25,445", "max-connections": "20"}})
	if job.Status != "success" {
		t.Fatal(job.Message)
	}
	records, err := readAuditRecords(a.statePath, 20)
	if err != nil || len(records) < 2 || records[len(records)-1].Actor != "web:admin" {
		t.Fatal("Web actor not audited")
	}
	job = performWebAction(t, b, q, webActionInput{Action: "user.add", Fields: map[string]string{"user": "bob", "node-name": "Local", "outbound": "direct"}})
	if job.Status != "success" {
		t.Fatal(job.Message)
	}
	after, _ = loadState(a.statePath)
	if findUser(after, "bob") == nil || findUser(after, "bob").Nodes[0].UUID == before.Users[0].Nodes[0].UUID {
		t.Fatal("new identity missing or reused")
	}
}

func TestWebInventoryAndErrorsHideCredentialsDeliveryRechecksState(t *testing.T) {
	a, b := webFixture(t)
	q := loginWebFixture(t, b)
	q.Method, q.Path = "GET", "/api/state"
	r := b.lookup(context.Background(), q)
	if r.Status != 200 {
		t.Fatalf("inventory %d", r.Status)
	}
	s, _ := loadState(a.statePath)
	for _, secret := range []string{s.Users[0].Nodes[0].UUID, s.Users[0].Devices[0].SubscriptionToken, b.config.PasswordHash, b.config.Salt, webTestPassword, "private_key", "raw_json"} {
		if bytes.Contains(r.Body, []byte(secret)) {
			t.Fatal("private value leaked in inventory")
		}
	}
	q.Method, q.Path, q.Body = "POST", "/api/delivery", []byte(`{"user":"alice","device":"phone","format":"link"}`)
	r = b.lookup(context.Background(), q)
	if r.Status != 200 || !bytes.Contains(r.Body, []byte(s.Users[0].Devices[0].SubscriptionToken)) || r.Filename == "" {
		t.Fatal("explicit delivery failed")
	}
	s.Users[0].Enabled = false
	if err := saveState(a.statePath, s); err != nil {
		t.Fatal(err)
	}
	if r = b.lookup(context.Background(), q); r.Status != 403 {
		t.Fatal("delivery ignored revocation")
	}
	job := performWebAction(t, b, q, webActionInput{Action: "proxy.add", Fields: map[string]string{"kind": "outbound", "json": `{"password":"sentinel-never-echo","type":"invalid"}`}})
	if job.Status != "failed" || strings.Contains(job.Message, "sentinel") {
		t.Fatal("proxy error disclosed input")
	}
}

func TestWebHTTPEmbeddedFilesHeadersLimitsAndCookie(t *testing.T) {
	_, b := webFixture(t)
	assets := fstest.MapFS{
		"index.html":                {Data: []byte(`<meta name="csp-nonce" content="__CSP_NONCE__"><script src="/assets/app-fixture.js"></script>`)},
		"assets/app-fixture.js":     {Data: []byte(`console.info("fixture")`)},
		"assets/app-fixture.css":    {Data: []byte(`body{margin:0}`)},
		"assets/font-fixture.woff2": {Data: []byte(`fixture-font`)},
	}
	server := newWebHTTPServerWithAssets("", b.config.Origin, b.lookup, assets)
	for _, path := range []string{"/", "/assets/app-fixture.js", "/assets/app-fixture.css", "/assets/font-fixture.woff2"} {
		w := httptest.NewRecorder()
		server.Handler.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:9090"+path, nil))
		if w.Code != 200 || w.Body.Len() == 0 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("asset %s invalid", path)
		}
	}
	for _, path := range []string{"/state.db", "/web-admin.json", "/../config.base.json", "/deploy/install-systemd.sh", "/assets/../index.html", "/assets/missing.js", "/assets/app.js.map", "/README.md"} {
		w := httptest.NewRecorder()
		server.Handler.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:9090"+path, nil))
		if w.Code != 404 {
			t.Fatal("private path served")
		}
	}
	data, _ := json.Marshal(map[string]string{"username": "admin", "password": webTestPassword})
	req := httptest.NewRequest("POST", b.config.Origin+"/api/login", bytes.NewReader(data))
	req.Header.Set("Origin", b.config.Origin)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Handler.ServeHTTP(w, req)
	cookies := w.Result().Cookies()
	if w.Code != 200 || len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("session cookie invalid")
	}
	req = httptest.NewRequest("POST", b.config.Origin+"/api/actions", strings.NewReader(strings.Repeat("x", webMaxRequest+1)))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	server.Handler.ServeHTTP(w, req)
	if w.Code != 413 {
		t.Fatal("unbounded request")
	}
}

func TestWebBrokerFramingAndCancellation(t *testing.T) {
	parent, child := net.Pipe()
	defer parent.Close()
	defer child.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = serveWebBroker(ctx, parent, func(_ context.Context, q webRequest) webReply { return webJSON(200, map[string]string{"path": q.Path}) })
	}()
	rpc := webRPC{conn: child}
	for range 2 {
		if r := rpc.lookup(ctx, webRequest{Path: "/api/state"}); r.Status != 200 {
			t.Fatal("framing desynchronized")
		}
	}
	cancel()
	if r := rpc.lookup(ctx, webRequest{}); r.Status != 503 {
		t.Fatal("cancel ignored")
	}
	if r := webJSON(200, strings.Repeat("x", webMaxReply)); r.Status != 413 {
		t.Fatal("unbounded broker response")
	}
}

func TestWebConfigCLIAndEmbeddedInstaller(t *testing.T) {
	a, b := webFixture(t)
	for _, change := range []func(*webConfig){func(c *webConfig) { c.Listen = "0.0.0.0:9090" }, func(c *webConfig) {
		c.Origin = (&url.URL{Scheme: "http", Host: "localhost", User: url.UserPassword("example", "test-only")}).String()
	}, func(c *webConfig) { c.Username = "bad\nname" }, func(c *webConfig) { c.PasswordHash = "bad" }} {
		c := b.config
		change(&c)
		if validateWebConfig(c) == nil {
			t.Fatal("unsafe config accepted")
		}
	}
	for _, args := range [][]string{{"ui"}, {"menu"}, {"admin", "simple-menu"}, {"admin", "upgrade"}} {
		if a.run(append([]string{"--state", a.statePath}, args...)) == nil {
			t.Fatal("removed UI or update command accepted")
		}
	}
	password := filepath.Join(t.TempDir(), "password.txt")
	if err := os.WriteFile(password, []byte(webTestPassword), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.webCmd([]string{"configure", "--password-file", password}); err != nil {
		t.Fatal(err)
	}
	if err := a.adminCmd([]string{"client", "set", "--server", "relay.example", "--port", "8443"}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := a.serviceCmd([]string{"install", "--output-dir", dir}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"install-systemd.sh", "path-lib.sh", "sbmgr.service.in", "deploy-release.sh"} {
		want, err := deploy.Assets.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("embedded installer %s differs", name)
		}
	}
}

func TestWebSlaveAuthorizationIsManagedByMaster(t *testing.T) {
	a, b := webFixture(t)
	q := loginWebFixture(t, b)
	s, _ := loadState(a.statePath)
	s.MeshAgent = MeshAgentState{Cluster: "test", Member: "relay", Identity: &MeshIdentity{Master: false}}
	if err := saveState(a.statePath, s); err != nil {
		t.Fatal(err)
	}
	job := performWebAction(t, b, q, webActionInput{Action: "user.set", Fields: map[string]string{"user": "alice", "quota": "40G"}})
	if job.Status != "failed" || !strings.Contains(job.Message, "主机") {
		t.Fatal("slave accepted local user mutation")
	}
	after, _ := loadState(a.statePath)
	if after.Users[0].QuotaBytes != s.Users[0].QuotaBytes {
		t.Fatal("slave authorization changed")
	}
	view, err := a.webSnapshot()
	if err != nil || view["role"] != "slave" {
		t.Fatal("slave role missing before first rollout")
	}
}

func TestWebStructuredEntrySettingsAndLogout(t *testing.T) {
	a, b := webFixture(t)
	q := loginWebFixture(t, b)
	for _, input := range []webActionInput{
		{Action: "mesh.init", Fields: map[string]string{"id": "control", "cluster": "demo"}},
		{Action: "mesh.entry", Fields: map[string]string{"id": "control", "server": "relay.example", "port": "443", "server_name": "example.com", "reality_public_key": strings.Repeat("A", 43)}},
	} {
		if job := performWebAction(t, b, q, input); job.Status != "success" {
			t.Fatal(job.Message)
		}
	}
	s, err := loadState(a.statePath)
	if err != nil || s.Mesh.Members[0].Client == nil || s.Mesh.Members[0].Client.Server != "relay.example" {
		t.Fatal("entry fields not persisted")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(a.statePath), ".web-entry-*.json"))
	if len(files) != 0 {
		t.Fatal("temporary input retained")
	}
	b.mu.Lock()
	b.busy = true
	b.mu.Unlock()
	q.Method, q.Path, q.Body = "POST", "/api/actions", []byte(`{"action":"backup.create"}`)
	if r := b.lookup(context.Background(), q); r.Status != 409 {
		t.Fatal("overlapping mutation accepted")
	}
	q.Path, q.Body = "/api/logout", []byte(`{}`)
	if r := b.lookup(context.Background(), q); r.Status != 200 || !r.Logout {
		t.Fatal("logout failed")
	}
	q.Method, q.Path, q.Body = "GET", "/api/state", nil
	if r := b.lookup(context.Background(), q); r.Status != 401 {
		t.Fatal("logout did not revoke session")
	}
}

func TestWebHTTPAssetNonceAndMissingBuild(t *testing.T) {
	_, backend := webFixture(t)
	assets := fstest.MapFS{"index.html": {Data: []byte(`<meta name="csp-nonce" content="__CSP_NONCE__">`)}}
	server := newWebHTTPServerWithAssets("", backend.config.Origin, backend.lookup, assets)
	previous := ""
	for i := 0; i < 2; i++ {
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, httptest.NewRequest("GET", "http://127.0.0.1:9090/", nil))
		csp := recorder.Header().Get("Content-Security-Policy")
		start := strings.Index(csp, "'nonce-")
		if start < 0 || strings.Contains(csp, "unsafe-inline") {
			t.Fatal("missing nonce or weakened style policy")
		}
		nonce := strings.SplitN(csp[start+7:], "'", 2)[0]
		if len(nonce) != 48 || nonce == previous || !strings.Contains(recorder.Body.String(), nonce) || strings.Contains(recorder.Body.String(), "__CSP_NONCE__") {
			t.Fatal("nonce not unique or not applied to HTML")
		}
		previous = nonce
	}
	server = newWebHTTPServerWithAssets("", backend.config.Origin, backend.lookup, nil)
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, httptest.NewRequest("GET", "http://127.0.0.1:9090/", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatal("unbuilt frontend must return an explicit unavailable response")
	}
}
