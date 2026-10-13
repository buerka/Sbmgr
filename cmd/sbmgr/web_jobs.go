package main

import (
	"errors"
	"strings"
	"time"
)

type webJobUncertainError struct{ error }

const webJobRetention = 30 * time.Minute
const webMaxJobs = 128

// Receipts contain no action arguments, account hashes or passwords. The exact
// submitting session and CSRF token may read its receipt after credential
// rotation revokes login. This grants no access to other management endpoints.
// Called with b.mu held, after the privileged origin validation.
func (b *webBackend) jobReceiptLocked(q webRequest, now time.Time) (webReply, bool) {
	for id, job := range b.jobs {
		if job.Status != "running" && now.After(job.expires) {
			delete(b.jobs, id)
		}
	}
	id := q.RequestID
	query := q.Method == "GET" && strings.HasPrefix(q.Path, "/api/jobs/")
	if query {
		id = strings.TrimPrefix(q.Path, "/api/jobs/")
	}
	if id == "" {
		return webReply{}, false
	}
	job, exists := b.jobs[id]
	if !exists {
		if query {
			return webError(404, "任务记录不存在或已过期；无法据此判断是否已保存，请检查当前设置后再操作"), true
		}
		return webReply{}, false
	}
	if job.owner != q.Session || (!query && (job.csrf != q.CSRF || job.path != q.Path)) {
		return webError(404, "任务不存在"), true
	}
	reply := webJSON(200, job)
	// Never clear the cookie on an old receipt: a newer login in another tab
	// must not be signed out by a delayed response.
	return reply, true
}

// There is one mutation slot and no hidden queue. Busy and receipt capacity are
// checked before retaining any input. New requests cannot evict live receipts.
func (b *webBackend) startJobLocked(q webRequest, title string, run func() (string, bool, error)) webReply {
	if b.busy.Load() {
		return webError(409, "上一项修改仍在执行，请等待结果；本次尚未执行")
	}
	if len(b.jobs) >= webMaxJobs {
		return webError(429, "任务记录已满，请稍后重试；本次尚未执行")
	}
	id := q.RequestID
	if id == "" {
		id = webRandom()
	}
	job := webJob{ID: id, Title: title, Status: "running", Message: "已受理，正在执行；无需重复提交", owner: q.Session, csrf: q.CSRF, path: q.Path, expires: time.Now().Add(webJobRetention)}
	b.jobs[id] = job
	b.busy.Store(true)
	go func() {
		finish := startPerformancePhase("web.job")
		message, logout, err := func() (message string, logout bool, err error) {
			defer func() {
				if recover() != nil {
					err = webJobUncertainError{errors.New("执行意外中断，结果待确认；请检查当前设置后再操作")}
				}
			}()
			return run()
		}()
		finish(err)
		b.mu.Lock()
		defer b.mu.Unlock()
		b.busy.Store(false)
		job.Status, job.Message, job.Logout = "success", message, logout
		if err != nil {
			job.Status, job.Message = "failed", err.Error()
			var uncertain webJobUncertainError
			if errors.As(err, &uncertain) {
				job.Status = "unknown"
			}
		}
		job.expires = time.Now().Add(webJobRetention)
		b.jobs[id] = job
	}()
	return webJSON(202, job)
}

func (b *webBackend) mutationBusy() bool { return b.busy.Load() }
