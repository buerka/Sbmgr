package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionServerExportsOnlyTokenDeviceAndRevokesOldLink(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	s := &State{
		Subscription: SubscriptionSettings{Enabled: true, Listen: "127.0.0.1:0", BaseURL: "https://sub.example.com"},
		Client:       ClientSettings{Server: "example.com", Port: 443, ServerName: "example.com", PublicKey: "pub", ShortID: "abcd"},
		Users: []User{{Name: "alice", Enabled: true, QuotaBytes: 100, ExtraQuotaBytes: 20, Upload: 12, Download: 34, Expires: "2030-12-31", Devices: []Device{{Name: "phone", Enabled: true}, {Name: "pc", Enabled: true}}, Nodes: []Node{
			{Name: "Relay A", Device: "phone", AuthUser: "alice-phone", UUID: "11111111-1111-4111-8111-111111111111"},
			{Name: "Relay A", Device: "pc", AuthUser: "alice-pc", UUID: "22222222-2222-4222-8222-222222222222"},
		}}},
	}
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	s, _ = loadState(statePath)
	oldToken := findDevice(&s.Users[0], "phone").SubscriptionToken
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	server, err := a.startSubscriptionServer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	base := "http://" + server.Addr
	response, err := http.Get(base + "/sub/" + oldToken)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "11111111-1111-4111-8111-111111111111") || strings.Contains(string(body), "22222222-2222-4222-8222-222222222222") {
		t.Fatalf("bad subscription response status=%d body=%s", response.StatusCode, body)
	}
	expectedExpiry := time.Date(2031, time.January, 1, 0, 0, 0, 0, applicationLocation()).Add(-time.Second).Unix()
	expectedUserInfo := fmt.Sprintf("upload=12; download=34; total=120; expire=%d", expectedExpiry)
	if got := response.Header.Get("Subscription-Userinfo"); got != expectedUserInfo {
		t.Fatalf("subscription userinfo = %q, want %q", got, expectedUserInfo)
	}
	if got := response.Header.Get("Profile-Update-Interval"); got != "1" {
		t.Fatalf("profile update interval = %q", got)
	}
	encodedTitle := strings.TrimPrefix(response.Header.Get("Profile-Title"), "base64:")
	decodedTitle, err := base64.StdEncoding.DecodeString(encodedTitle)
	if err != nil || string(decodedTitle) != "alice-phone" {
		t.Fatalf("profile title = %q (%q, %v)", response.Header.Get("Profile-Title"), decodedTitle, err)
	}
	_, disposition, err := mime.ParseMediaType(response.Header.Get("Content-Disposition"))
	if err != nil || !regexp.MustCompile(`^alice-phone-\d{8}-\d{6}\.yaml$`).MatchString(disposition["filename"]) {
		t.Fatalf("content disposition = %q (%#v, %v)", response.Header.Get("Content-Disposition"), disposition, err)
	}
	headResponse, err := http.Head(base + "/sub/" + oldToken)
	if err != nil {
		t.Fatal(err)
	}
	headBody, _ := io.ReadAll(headResponse.Body)
	_ = headResponse.Body.Close()
	if headResponse.StatusCode != http.StatusOK || len(headBody) != 0 || headResponse.Header.Get("Subscription-Userinfo") != expectedUserInfo {
		t.Fatalf("HEAD subscription status=%d body=%d userinfo=%q", headResponse.StatusCode, len(headBody), headResponse.Header.Get("Subscription-Userinfo"))
	}
	qrResponse, err := http.Get(base + "/qr/" + oldToken + ".png")
	if err != nil {
		t.Fatal(err)
	}
	qrBody, _ := io.ReadAll(qrResponse.Body)
	_ = qrResponse.Body.Close()
	if qrResponse.StatusCode != http.StatusOK || qrResponse.Header.Get("Content-Type") != "image/png" || len(qrBody) < 100 {
		t.Fatalf("bad QR response status=%d type=%s size=%d", qrResponse.StatusCode, qrResponse.Header.Get("Content-Type"), len(qrBody))
	}
	if err := a.deviceCmd([]string{"rotate-link", "alice", "phone"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	oldResponse, err := http.Get(base + "/sub/" + oldToken)
	if err != nil {
		t.Fatal(err)
	}
	_ = oldResponse.Body.Close()
	if oldResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("old token still worked: %d", oldResponse.StatusCode)
	}
}

func TestSubscriptionMetadataHandlesUnlimitedAndUnicodeTitle(t *testing.T) {
	userinfo, err := subscriptionUserInfo(User{Upload: 7, Download: 11, QuotaBytes: 0, ExtraQuotaBytes: 99})
	if err != nil {
		t.Fatal(err)
	}
	if userinfo != "upload=7; download=11; total=0; expire=0" {
		t.Fatalf("unlimited userinfo = %q", userinfo)
	}
	response := httptest.NewRecorder()
	now := time.Date(2026, time.September, 3, 4, 5, 6, 0, time.UTC)
	if err := setSubscriptionProfileHeadersAt(response, User{Name: "测试用户"}, Device{Name: "手机/平板"}, now); err != nil {
		t.Fatal(err)
	}
	encodedTitle := strings.TrimPrefix(response.Header().Get("Profile-Title"), "base64:")
	title, err := base64.StdEncoding.DecodeString(encodedTitle)
	if err != nil || string(title) != "测试用户-手机-平板" {
		t.Fatalf("unicode profile title = %q (%q, %v)", response.Header().Get("Profile-Title"), title, err)
	}
	_, disposition, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
	if err != nil || disposition["filename"] != "测试用户-手机-平板-20260903-120506.yaml" {
		t.Fatalf("unicode content disposition = %q (%#v, %v)", response.Header().Get("Content-Disposition"), disposition, err)
	}
}

func TestSubscriptionUserInfoUsesSelectedQuotaModeWithoutMutatingCounters(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want string
	}{
		{name: "legacy empty is total", mode: "", want: "upload=70; download=30; total=200; expire=0"},
		{name: "total", mode: quotaModeTotal, want: "upload=70; download=30; total=200; expire=0"},
		{name: "upload", mode: quotaModeUpload, want: "upload=70; download=0; total=200; expire=0"},
		{name: "download", mode: quotaModeDownload, want: "upload=0; download=30; total=200; expire=0"},
		{name: "invalid falls back to total", mode: "invalid", want: "upload=70; download=30; total=200; expire=0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			u := User{QuotaBytes: 200, QuotaMode: test.mode, Upload: 70, Download: 30}
			got, err := subscriptionUserInfo(u)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("Subscription-Userinfo = %q, want %q", got, test.want)
			}
			if u.Upload != 70 || u.Download != 30 {
				t.Fatalf("reporting mutated raw counters: %#v", u)
			}
			reportedUpload, reportedDownload := subscriptionReportedUsage(u)
			if reportedUpload+reportedDownload != measuredUsage(u) {
				t.Fatalf("reported numerator %d does not match measured usage %d", reportedUpload+reportedDownload, measuredUsage(u))
			}
		})
	}
}

func TestSubscriptionHEADDoesNotRenderMihomoTemplate(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	state := &State{
		Subscription: SubscriptionSettings{Enabled: true, Listen: "127.0.0.1:0"},
		Client:       ClientSettings{MihomoTemplate: filepath.Join(t.TempDir(), "missing.yaml")},
		Users: []User{{Name: "alice", Enabled: true, Devices: []Device{{Name: "phone", Enabled: true}}, Nodes: []Node{
			{Name: "Relay A", Device: "phone", AuthUser: "alice-phone", UUID: "11111111-1111-4111-8111-111111111111"},
		}}},
	}
	if err := saveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	state, _ = loadState(statePath)
	token := state.Users[0].Devices[0].SubscriptionToken
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	server, err := a.startSubscriptionServer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	response, err := http.Head("http://" + server.Addr + "/sub/" + token)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Subscription-Userinfo") == "" {
		t.Fatalf("HEAD status=%d userinfo=%q", response.StatusCode, response.Header.Get("Subscription-Userinfo"))
	}
	qrResponse, err := http.Head("http://" + server.Addr + "/qr/" + token + ".png")
	if err != nil {
		t.Fatal(err)
	}
	_ = qrResponse.Body.Close()
	if qrResponse.StatusCode != http.StatusMethodNotAllowed || qrResponse.Header.Get("Allow") != "GET" {
		t.Fatalf("QR HEAD status=%d allow=%q", qrResponse.StatusCode, qrResponse.Header.Get("Allow"))
	}
}

func TestSubscriptionClientIPTrustsOnlyLoopbackProxy(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://example/sub/token", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Forwarded-For", "192.0.2.66, 203.0.113.10")
	request.Header.Set("X-Real-IP", "203.0.113.10")
	if got := subscriptionClientIP(request); got != "203.0.113.10" {
		t.Fatalf("loopback proxy IP = %q", got)
	}
	request.Header.Del("X-Real-IP")
	if got := subscriptionClientIP(request); got != "127.0.0.1" {
		t.Fatalf("spoofable X-Forwarded-For was trusted: %q", got)
	}
	request.RemoteAddr = "198.51.100.20:1234"
	request.Header.Set("X-Real-IP", "203.0.113.11")
	if got := subscriptionClientIP(request); got != "198.51.100.20" {
		t.Fatalf("public client spoofed forwarded IP: %q", got)
	}
}

func TestPublicSubscriptionURLRequiresHTTPS(t *testing.T) {
	if err := validateSubscriptionRuntime(SubscriptionSettings{Listen: "127.0.0.1:18080", BaseURL: "http://sub.example.com"}); err == nil {
		t.Fatal("public HTTP subscription URL was accepted")
	}
	if err := validateSubscriptionRuntime(SubscriptionSettings{Listen: "127.0.0.1:18080", BaseURL: "http://127.0.0.1:18080"}); err != nil {
		t.Fatal("loopback HTTP subscription URL was rejected:", err)
	}
}

func TestSubscriptionTokensAreUniqueAndPersisted(t *testing.T) {
	s := &State{Users: []User{{Name: "alice", Devices: []Device{{Name: "phone", Enabled: true}, {Name: "pc", Enabled: true}}}}}
	normalizeDeviceModel(s)
	first := s.Users[0].Devices[0].SubscriptionToken
	second := s.Users[0].Devices[1].SubscriptionToken
	if len(first) < 20 || len(second) < 20 || first == second {
		t.Fatalf("invalid subscription tokens: %q %q", first, second)
	}
}

func TestLegacyMissingSubscriptionTokenIsCanonicalizedBeforeHTTPReads(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	raw := `{
  "version": 2,
  "client": {"server":"example.com","port":443,"server_name":"example.com","reality_public_key":"pub","short_id":"abcd"},
  "subscription": {"enabled":true,"listen":"127.0.0.1:0"},
  "users": [{"name":"alice","enabled":true,"nodes":[{"name":"Relay A","auth_user":"alice:relay-a","uuid":"11111111-1111-4111-8111-111111111111"}]}]
}`
	if err := os.WriteFile(statePath, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := a.startSubscriptionServer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	firstLoad, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	firstToken := firstLoad.Users[0].Devices[0].SubscriptionToken
	secondLoad, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstToken) < 20 || firstLoad.Users[0].Devices[0].SubscriptionToken != firstToken || secondLoad.Users[0].Devices[0].SubscriptionToken != firstToken {
		t.Fatalf("canonical token was not stable across loads: %q %q %q", firstToken, firstLoad.Users[0].Devices[0].SubscriptionToken, secondLoad.Users[0].Devices[0].SubscriptionToken)
	}

	response, err := http.Get("http://" + server.Addr + "/sub/" + firstToken)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "11111111-1111-4111-8111-111111111111") {
		t.Fatalf("canonical legacy token was not served: status=%d body=%s", response.StatusCode, body)
	}
}

func TestRunningSubscriptionServerHonorsDisableWithoutRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	state := &State{
		Subscription: SubscriptionSettings{Enabled: true, Listen: "127.0.0.1:0"},
		Client:       ClientSettings{Server: "example.com", Port: 443, ServerName: "example.com", PublicKey: "pub", ShortID: "abcd"},
		Users: []User{{Name: "alice", Enabled: true, Devices: []Device{{Name: "phone", Enabled: true}}, Nodes: []Node{{
			Name: "Relay A", Device: "phone", AuthUser: "alice:relay-a", UUID: "11111111-1111-4111-8111-111111111111",
		}}}},
	}
	if err := saveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	stored, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	token := stored.Users[0].Devices[0].SubscriptionToken
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := a.startSubscriptionServer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	if err := a.subscriptionCmdLocked([]string{"set", "--enabled", "false"}); err != nil {
		t.Fatal(err)
	}
	response, err := http.Get("http://" + server.Addr + "/sub/" + token)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable || strings.Contains(string(body), "11111111-1111-4111-8111-111111111111") {
		t.Fatalf("disabled live server still delivered subscription: status=%d body=%s", response.StatusCode, body)
	}
}
