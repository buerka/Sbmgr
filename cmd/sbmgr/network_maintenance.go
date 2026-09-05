package main

import (
	"errors"
	"reflect"
	"time"
)

// Snapshot under the state lock, perform network I/O without it, then merge
// only still-current results. A concurrent user edit can never be overwritten.
func (a *app) networkMaintenance() error {
	var snapshot *State
	if err := a.withStateLock(func() error { var err error; snapshot, err = loadState(a.statePath); return err }); err != nil {
		return err
	}
	now := time.Now()
	beforeHealth := snapshot.LastHealthCheck
	beforeAlerts := append([]Alert(nil), snapshot.Alerts...)
	beforeFleet := snapshot.FleetStatus
	// refreshFleet mutates its map; retain the previous values for comparison.
	snapshot.FleetStatus = make(map[string]FleetServerStatus, len(beforeFleet))
	for k, v := range beforeFleet {
		snapshot.FleetStatus[k] = v
	}
	healthChanged, healthErr := checkOutboundHealth(snapshot, now, false)
	fleetChanged := fleetCheckDue(snapshot, now) && refreshFleet(snapshot, now)
	notifyChanged, notifyErr := deliverPendingAlerts(snapshot, now)
	if !healthChanged && !fleetChanged && !notifyChanged {
		return errors.Join(healthErr, notifyErr)
	}
	mergeErr := a.withStateLock(func() error {
		current, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		healthCurrent := reflect.DeepEqual(current.Health, snapshot.Health) && current.BaseConfig == snapshot.BaseConfig && current.LastHealthCheck == beforeHealth
		fleetCurrent := reflect.DeepEqual(current.Fleet, snapshot.Fleet) && reflect.DeepEqual(current.FleetStatus, beforeFleet)
		notifyCurrent := current.Notifications == snapshot.Notifications
		changed := false
		if healthChanged && healthCurrent {
			current.OutboundHealth, current.LastHealthCheck = snapshot.OutboundHealth, snapshot.LastHealthCheck
			changed = true
		}
		if fleetChanged && fleetCurrent {
			current.FleetStatus = snapshot.FleetStatus
			changed = true
		}
		prior := map[string]Alert{}
		for _, alert := range beforeAlerts {
			key, _ := sqliteAlertIdentity(alert)
			prior[key] = alert
		}
		results := map[string]Alert{}
		for _, alert := range snapshot.Alerts {
			key, _ := sqliteAlertIdentity(alert)
			results[key] = alert
			if _, old := prior[key]; !old && ((healthCurrent && (alert.Kind == "outbound_recovered" || alert.Kind == "outbound_unhealthy")) || (fleetCurrent && (alert.Kind == "fleet_offline" || alert.Kind == "fleet_recovered"))) {
				appendAlert(current, alert)
				changed = true
			}
		}
		if notifyCurrent {
			for i := range current.Alerts {
				alert := &current.Alerts[i]
				key, _ := sqliteAlertIdentity(*alert)
				old, ok := prior[key]
				result, found := results[key]
				if ok && found && alert.LastNotifyAttempt == old.LastNotifyAttempt && alert.NotifyAttempts == old.NotifyAttempts && result.NotifyAttempts > old.NotifyAttempts {
					alert.NotifiedAt, alert.NotifyAttempts, alert.LastNotifyAttempt, alert.NotifyError = result.NotifiedAt, result.NotifyAttempts, result.LastNotifyAttempt, result.NotifyError
					changed = true
				}
			}
		}
		if changed {
			return saveState(a.statePath, current)
		}
		return nil
	})
	return errors.Join(healthErr, notifyErr, mergeErr)
}
