package main

import (
	"context"
	"os"
	"time"
)

// The supervisor follows saved configuration changes without holding the state
// lock during the streaming RPC. A failed stream reconnects with a fresh reset.
func (a *app) startDaemonAnalytics(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		var current ConnectionAnalyticsSettings
		var cancel context.CancelFunc
		var lastMaintenance time.Time
		check := func() {
			s, err := loadState(a.statePath)
			if err != nil {
				return
			}
			// Retention and revoked-identity cleanup continue after collection is
			// disabled. They use only the analytics lock, never the business lock.
			if time.Since(lastMaintenance) >= time.Minute {
				if _, err := os.Stat(analyticsDatabasePath(a.statePath)); err == nil {
					if err := maintainAnalyticsStorage(a.statePath); err == nil {
						lastMaintenance = time.Now()
					}
				} else if os.IsNotExist(err) {
					lastMaintenance = time.Now()
				}
			}
			wanted := ConnectionAnalyticsSettings{}
			if s.Analytics != nil && s.Analytics.Enabled {
				wanted = *s.Analytics
			}
			if wanted == current {
				return
			}
			if cancel != nil {
				cancel()
				cancel = nil
			}
			current = wanted
			if !wanted.Enabled {
				return
			}
			streamCtx, stop := context.WithCancel(ctx)
			cancel = stop
			go func(settings ConnectionAnalyticsSettings) {
				collector := &app{statePath: a.statePath, out: a.out, err: a.err, actor: a.actor}
				for streamCtx.Err() == nil {
					_ = watchConnectionStream(streamCtx, settings.Listen, settings.Secret, collector.ingestAnalytics)
					select {
					case <-streamCtx.Done():
						return
					case <-time.After(5 * time.Second):
					}
				}
			}(wanted)
		}
		check()
		for {
			select {
			case <-ctx.Done():
				if cancel != nil {
					cancel()
				}
				return
			case <-ticker.C:
				check()
			}
		}
	}()
}
