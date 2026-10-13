package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Fixed phase names bound memory and prevent user input or secrets from being
// used as metric labels. Durations are observational, never transaction timers.
var performancePhases = map[string]bool{
	"state.load": true, "state.save": true, "state.hash": true,
	"state.backup": true, "state.integrity": true, "state.write": true,
	"lock.wait": true, "lock.hold": true, "daemon.maintenance": true,
	"daemon.realtime": true, "mesh.sync": true, "analytics.ingest": true,
	"config.render": true, "config.check": true, "config.apply": true,
	"config.reload": true, "web.request": true, "web.job": true, "web.password": true,
}

var performanceLimits = [...]time.Duration{
	time.Millisecond, 10 * time.Millisecond, 50 * time.Millisecond,
	100 * time.Millisecond, 250 * time.Millisecond, time.Second,
	5 * time.Second, 20 * time.Second, 30 * time.Second,
}

type performanceCounts struct {
	Calls, Errors, Busy, Canceled uint64
	Active, Peak                  uint64
	Total, Max                    time.Duration
	Buckets                       [len(performanceLimits) + 1]uint64
}

var performanceStats = struct {
	sync.Mutex
	counts map[string]*performanceCounts
}{counts: make(map[string]*performanceCounts)}

var performanceSequence atomic.Uint64

func performanceErrorClass(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	// Inspect errors only for classification; never print their payloads.
	if strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "SQLITE_LOCKED") {
		return "busy"
	}
	return "error"
}

func startPerformancePhase(stage string) func(error) {
	if !performancePhases[stage] {
		return func(error) {}
	}
	started := time.Now()
	id := performanceSequence.Add(1)
	performanceStats.Lock()
	c := performanceStats.counts[stage]
	if c == nil {
		c = &performanceCounts{}
		performanceStats.counts[stage] = c
	}
	c.Active++
	c.Peak = max(c.Peak, c.Active)
	performanceStats.Unlock()
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			elapsed := time.Since(started)
			class := performanceErrorClass(err)
			performanceStats.Lock()
			c.Active--
			c.Calls++
			c.Total += elapsed
			c.Max = max(c.Max, elapsed)
			bucket := len(performanceLimits)
			for i, limit := range performanceLimits {
				if elapsed <= limit {
					bucket = i
					break
				}
			}
			c.Buckets[bucket]++
			if err != nil {
				c.Errors++
			}
			if class == "busy" {
				c.Busy++
			}
			if class == "canceled" {
				c.Canceled++
			}
			performanceStats.Unlock()
			if elapsed >= time.Second || err != nil {
				fmt.Fprintf(os.Stderr, "sbmgr_perf phase=%s span=%d duration_ms=%d result=%s\n", stage, id, elapsed.Milliseconds(), class)
			}
		})
	}
}

// Called once per minute by the maintenance loop; bins allow operators to
// compare latency distributions without storing per-request histories.
func reportPerformance(w io.Writer) {
	if w == nil {
		return
	}
	performanceStats.Lock()
	snapshot := make(map[string]performanceCounts, len(performanceStats.counts))
	for name, c := range performanceStats.counts {
		snapshot[name] = *c
	}
	performanceStats.Unlock()
	keys := make([]string, 0, len(snapshot))
	for name := range snapshot {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		c := snapshot[name]
		fmt.Fprintf(w, "sbmgr_perf_summary phase=%s calls=%d errors=%d busy=%d canceled=%d active=%d peak=%d total_ms=%d max_ms=%d bins=%v\n", name, c.Calls, c.Errors, c.Busy, c.Canceled, c.Active, c.Peak, c.Total.Milliseconds(), c.Max.Milliseconds(), c.Buckets)
	}
}
