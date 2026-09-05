package main

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const subscriptionRateCapacity = 4096

var subscriptionTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{20,128}$`)

type subscriptionLimiter struct {
	mu     sync.Mutex
	rates  map[string]subscriptionRateEntry
	global subscriptionRateEntry
	pruned time.Time
}

func (l *subscriptionLimiter) allow(peer string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rates == nil {
		l.rates = make(map[string]subscriptionRateEntry)
	}
	if now.Sub(l.pruned) >= time.Minute {
		for k, v := range l.rates {
			if now.Sub(v.window) >= time.Minute {
				delete(l.rates, k)
			}
		}
		l.pruned = now
	}
	if l.global.window.IsZero() || now.Sub(l.global.window) >= time.Minute {
		l.global = subscriptionRateEntry{window: now}
	}
	if l.global.count >= 600 {
		return false
	}
	e, exists := l.rates[peer]
	if !exists && len(l.rates) >= subscriptionRateCapacity {
		return false
	}
	if e.window.IsZero() || now.Sub(e.window) >= time.Minute {
		e = subscriptionRateEntry{window: now}
	}
	if e.count >= 60 {
		return false
	}
	e.count++
	l.rates[peer] = e
	l.global.count++
	return true
}

// This indexed read sees committed WAL changes immediately. No cached bearer
// credentials and no full history loading for an unknown token.
func subscriptionTokenExists(ctx context.Context, path, token string) (bool, error) {
	if !subscriptionTokenPattern.MatchString(token) {
		return false, nil
	}
	if !isSQLiteStatePath(path) {
		// Explicit legacy JSON compatibility only; deployments use SQLite.
		s, err := loadState(path)
		if err != nil {
			return false, err
		}
		u, d := findSubscriptionDevice(s, token)
		return u != nil && d != nil, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	p := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	dsn := (&url.URL{Scheme: "file", Path: p, RawQuery: "mode=ro&_pragma=trusted_schema%3DOFF"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return false, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var exists bool
	err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE subscription_token = ?)`, token).Scan(&exists)
	return exists, err
}
