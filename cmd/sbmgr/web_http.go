package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

//go:embed web
var webAssets embed.FS

const webMaxRequest = 256 << 10
const webMaxReply = 8 << 20
const webCookie = "sbmgr_session"

type webRequest struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Origin  string `json:"origin"`
	Host    string `json:"host"`
	Remote  string `json:"remote"`
	Session string `json:"session"`
	CSRF    string `json:"csrf"`
	Body    []byte `json:"body,omitempty"`
}
type webReply struct {
	Status     int    `json:"status"`
	Body       []byte `json:"body"`
	Type       string `json:"type,omitempty"`
	SetSession string `json:"set_session,omitempty"`
	Logout     bool   `json:"logout,omitempty"`
	Filename   string `json:"filename,omitempty"`
}
type webLookup func(context.Context, webRequest) webReply
type webSession struct {
	CSRF    string
	Expires time.Time
}
type webAttempt struct {
	Count int
	Until time.Time
}
type webJob struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Message string `json:"message"`
}
type webBackend struct {
	a        *app
	config   webConfig
	mu       sync.Mutex
	sessions map[string]webSession
	attempts map[string]webAttempt
	jobs     map[string]webJob
	busy     bool
}

func newWebBackend(a *app, c webConfig) *webBackend {
	return &webBackend{a: a, config: c, sessions: map[string]webSession{}, attempts: map[string]webAttempt{}, jobs: map[string]webJob{}}
}
func webRandom() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func webJSON(status int, value any) webReply {
	b, err := json.Marshal(value)
	if err != nil {
		return webError(500, "响应生成失败")
	}
	if len(b) > webMaxReply/2 {
		return webError(413, "管理数据过大，请使用 CLI 按用户查询")
	}
	return webReply{Status: status, Body: b, Type: "application/json; charset=utf-8"}
}
func webError(status int, message string) webReply {
	return webJSON(status, map[string]string{"error": message})
}
func webDecode(data []byte, target any) error {
	if len(data) > webMaxRequest {
		return errors.New("请求过大")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return errors.New("请求格式不正确")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("请求只能包含一个 JSON 对象")
	}
	return nil
}

func (b *webBackend) lookup(ctx context.Context, q webRequest) webReply {
	if ctx.Err() != nil {
		return webError(503, "请求已取消")
	}
	if len(q.Body) > webMaxRequest || len(q.Path) > 512 || len(q.Session) > 64 || len(q.CSRF) > 64 || len(q.Remote) > 128 {
		return webError(400, "请求过大")
	}
	// Check at the privileged boundary too: the HTTP worker has no authority
	// to select files, SQL, shell commands or unauthenticated operations.
	c, err := readWebConfig(b.a.statePath)
	if err != nil {
		return webError(503, "管理设置不可用")
	}
	requestPath, ok := webRelativePath(q.Path, c.BasePath)
	if !ok {
		return webError(404, "接口不存在")
	}
	q.Path = requestPath
	u, _ := url.Parse(c.Origin)
	if q.Host != u.Host || (q.Method != "GET" && q.Origin != c.Origin) || (q.Origin != "" && q.Origin != c.Origin) {
		return webError(403, "请求来源不匹配")
	}
	b.mu.Lock()
	if c.PasswordHash != b.config.PasswordHash || c.Username != b.config.Username || c.Origin != b.config.Origin || c.BasePath != b.config.BasePath {
		clear(b.sessions)
	}
	b.config = c
	now := time.Now()
	for id, s := range b.sessions {
		if !now.Before(s.Expires) {
			delete(b.sessions, id)
		}
	}
	if q.Path == "/api/login" && q.Method == "POST" {
		defer b.mu.Unlock()
		for ip, a := range b.attempts {
			if now.After(a.Until) {
				delete(b.attempts, ip)
			}
		}
		attempt := b.attempts[q.Remote]
		global := b.attempts["*"]
		if attempt.Count >= 5 || global.Count >= 60 || len(b.attempts) >= 1024 {
			return webError(429, "登录尝试过于频繁，请稍后重试")
		}
		var input struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if webDecode(q.Body, &input) != nil || len(input.Password) > 1024 || len(input.Username) > 64 {
			return webError(400, "登录信息格式不正确")
		}
		hash, err := webPasswordHash(input.Password, c.Salt)
		if err != nil || subtle.ConstantTimeCompare([]byte(hash), []byte(c.PasswordHash)) != 1 || subtle.ConstantTimeCompare([]byte(input.Username), []byte(c.Username)) != 1 {
			if attempt.Count == 0 {
				attempt.Until = now.Add(5 * time.Minute)
			}
			attempt.Count++
			b.attempts[q.Remote] = attempt
			if global.Count == 0 {
				global.Until = now.Add(5 * time.Minute)
			}
			global.Count++
			b.attempts["*"] = global
			return webError(401, "账号或密码错误")
		}
		delete(b.attempts, q.Remote)
		if len(b.sessions) >= 32 {
			return webError(429, "登录会话过多，请稍后重试")
		}
		id, csrf := webRandom(), webRandom()
		b.sessions[id] = webSession{csrf, now.Add(8 * time.Hour)}
		r := webJSON(200, map[string]string{"csrf": csrf, "username": c.Username})
		r.SetSession = id
		return r
	}
	session, ok := b.sessions[q.Session]
	if !ok {
		b.mu.Unlock()
		return webError(401, "请登录管理面板")
	}
	if q.Method != "GET" && subtle.ConstantTimeCompare([]byte(session.CSRF), []byte(q.CSRF)) != 1 {
		b.mu.Unlock()
		return webError(403, "操作校验已失效，请刷新页面")
	}
	if q.Path == "/api/session" && q.Method == "GET" {
		b.mu.Unlock()
		return webJSON(200, map[string]string{"csrf": session.CSRF, "username": c.Username})
	}
	if q.Path == "/api/logout" && q.Method == "POST" {
		delete(b.sessions, q.Session)
		b.mu.Unlock()
		r := webJSON(200, map[string]bool{"ok": true})
		r.Logout = true
		return r
	}
	if strings.HasPrefix(q.Path, "/api/jobs/") && q.Method == "GET" {
		j, ok := b.jobs[strings.TrimPrefix(q.Path, "/api/jobs/")]
		b.mu.Unlock()
		if !ok {
			return webError(404, "任务不存在")
		}
		return webJSON(200, j)
	}
	b.mu.Unlock()
	switch {
	case q.Path == "/api/state" && q.Method == "GET":
		state, err := b.a.webSnapshot()
		if err != nil {
			return webError(503, "读取管理状态失败；请检查初始化与数据库")
		}
		return webJSON(200, state)
	case q.Path == "/api/catalog" && q.Method == "GET":
		return webJSON(200, webActions())
	case q.Path == "/api/actions" && q.Method == "POST":
		var input webActionInput
		if err := webDecode(q.Body, &input); err != nil {
			return webError(400, err.Error())
		}
		args, spec, err := compileWebAction(input)
		if err != nil {
			return webError(400, err.Error())
		}
		b.mu.Lock()
		if b.busy {
			b.mu.Unlock()
			return webError(409, "上一项修改仍在执行，请等待结果")
		}
		b.busy = true
		if len(b.jobs) >= 32 {
			clear(b.jobs)
		}
		job := webJob{ID: webRandom(), Title: spec.Title, Status: "running", Message: "正在执行"}
		b.jobs[job.ID] = job
		b.mu.Unlock()
		go func() {
			var warnings bytes.Buffer
			a := &app{statePath: b.a.statePath, out: io.Discard, err: &warnings, actor: "web:" + c.Username}
			err := a.executeWebAction(input, args)
			b.mu.Lock()
			defer b.mu.Unlock()
			b.busy = false
			job.Status = "success"
			job.Message = spec.Effect
			if warnings.Len() != 0 {
				job.Message += " 操作已完成，但审计或附加步骤存在告警；请检查服务器运行状态。"
			}
			if err != nil {
				job.Status = "failed"
				job.Message = safeWebActionError(err, input)
			}
			b.jobs[job.ID] = job
		}()
		return webJSON(202, job)
	case q.Path == "/api/delivery" && q.Method == "POST":
		return b.a.webDelivery(q.Body)
	default:
		return webError(404, "接口不存在")
	}
}

func webRelativePath(requestPath, basePath string) (string, bool) {
	if !strings.HasPrefix(requestPath, basePath+"/") || strings.Contains(requestPath, "\\") || strings.Contains(requestPath, "//") || (requestPath != basePath+"/" && path.Clean(requestPath) != requestPath) {
		return "", false
	}
	return strings.TrimPrefix(requestPath, basePath), true
}

func newWebHTTPServer(address, origin, basePath string, lookup webLookup) *http.Server {
	assets, _ := fs.Sub(webAssets, "web/dist")
	return newWebHTTPServerWithAssets(address, origin, basePath, lookup, assets)
}

func newWebHTTPServerWithAssets(address, origin, basePath string, lookup webLookup, assets fs.FS) *http.Server {
	secure := strings.HasPrefix(origin, "https://")
	slots := make(chan struct{}, 16)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
		requestPath, ok := webRelativePath(r.URL.Path, basePath)
		// Reject alternate encodings rather than turning the path into an alias.
		if !ok || r.URL.EscapedPath() != r.URL.Path {
			if basePath != "" && r.URL.Path == basePath && r.URL.EscapedPath() == basePath && (r.Method == "GET" || r.Method == "HEAD") {
				http.Redirect(w, r, basePath+"/", http.StatusPermanentRedirect)
				return
			}
			http.NotFound(w, r)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.WriteHeader(429)
			return
		}
		nonceBytes := make([]byte, 24)
		if _, err := rand.Read(nonceBytes); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		nonce := hex.EncodeToString(nonceBytes)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'nonce-"+nonce+"'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(requestPath, "/api/") {
			if r.Method != "GET" && r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			if r.Method == "POST" && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				w.WriteHeader(415)
				return
			}
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, webMaxRequest))
			if err != nil {
				w.WriteHeader(413)
				return
			}
			session := ""
			if cookie, err := r.Cookie(webCookie); err == nil {
				session = cookie.Value
			}
			q := webRequest{Method: r.Method, Path: r.URL.Path, Origin: r.Header.Get("Origin"), Host: r.Host, Remote: subscriptionClientIP(r), Session: session, CSRF: r.Header.Get("X-CSRF-Token"), Body: body}
			response := lookup(r.Context(), q)
			if response.SetSession != "" {
				http.SetCookie(w, &http.Cookie{Name: webCookie, Value: response.SetSession, Path: basePath + "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 60 * 60})
			}
			if response.Logout {
				http.SetCookie(w, &http.Cookie{Name: webCookie, Path: basePath + "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
			}
			w.Header().Set("Content-Type", response.Type)
			if response.Filename != "" {
				w.Header().Set("Content-Disposition", "attachment; filename=\""+response.Filename+"\"")
			}
			w.WriteHeader(response.Status)
			_, _ = w.Write(response.Body)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		file, mime := "", ""
		if requestPath == "/" {
			file, mime = "index.html", "text/html; charset=utf-8"
		} else if strings.HasPrefix(requestPath, "/assets/") {
			file = strings.TrimPrefix(requestPath, "/")
			if !fs.ValidPath(file) || strings.Contains(file, "\\") {
				http.NotFound(w, r)
				return
			}
			switch path.Ext(file) {
			case ".js":
				mime = "text/javascript; charset=utf-8"
			case ".css":
				mime = "text/css; charset=utf-8"
			case ".woff":
				mime = "font/woff"
			case ".woff2":
				mime = "font/woff2"
			case ".svg":
				mime = "image/svg+xml"
			default:
				http.NotFound(w, r)
				return
			}
		} else {
			http.NotFound(w, r)
			return
		}
		var data []byte
		var err error
		if assets == nil {
			err = fs.ErrNotExist
		} else {
			data, err = fs.ReadFile(assets, file)
		}
		if err != nil {
			if file == "index.html" {
				http.Error(w, "Web assets are not built. Run npm --prefix frontend ci && npm --prefix frontend run build before go build.", http.StatusServiceUnavailable)
			} else {
				http.NotFound(w, r)
			}
			return
		}
		if file == "index.html" {
			data = bytes.ReplaceAll(data, []byte("__CSP_NONCE__"), []byte(nonce))
		}

		w.Header().Set("Content-Type", mime)
		if r.Method != "HEAD" {
			_, _ = w.Write(data)
		}
	})
	return &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384, ErrorLog: log.New(io.Discard, "", 0), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
}
