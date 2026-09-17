package main

import (
	"fmt"

	"testing"
	"time"
)

func TestRecentAccessArchiveAggregatesFiltersAndPrunes(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	u := User{Name: "alice"}
	phone := &Device{Name: "phone"}
	recordRecentAccess(&u, phone, "Node A", "service.example", now)
	recordRecentAccess(&u, phone, "Node A", "service.example", now.Add(time.Minute))
	recordRecentAccess(&u, &Device{Name: "pc"}, "Relay A", "example.com", now.Add(2*time.Minute))
	u.RecentAccesses = recentAccessesForUser(&u, "")
	if len(u.RecentAccesses) != 2 || u.RecentAccesses[1].Count != 2 || u.RecentAccesses[1].LastSeen != now.Add(time.Minute).Format(time.RFC3339) {
		t.Fatalf("archive did not aggregate/sort: %#v", u.RecentAccesses)
	}
	for query, target := range map[string]string{"service": "service.example", "phone": "service.example", "relay": "example.com"} {
		values := recentAccessesForUser(&u, query)
		if len(values) != 1 || values[0].Target != target {
			t.Fatalf("filter %q = %#v", query, values)
		}
	}
	u.RecentAccesses = append(u.RecentAccesses, RecentAccess{Target: "old.example", LastSeen: now.Add(-recentAccessWindow - time.Second).Format(time.RFC3339), Count: 1})
	if !pruneRecentAccesses(&u, now.Add(3*time.Minute)) || len(u.RecentAccesses) != 2 {
		t.Fatalf("old archive entry was not pruned: %#v", u.RecentAccesses)
	}
}

func TestRecentAccessArchiveIsBounded(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	u := User{Name: "alice"}
	for index := 0; index < recentAccessLimit+5; index++ {
		recordRecentAccess(&u, nil, "Node A", fmt.Sprintf("site-%04d.example", index), now.Add(time.Duration(index)*time.Second))
	}
	if len(u.RecentAccesses) != recentAccessLimit {
		t.Fatalf("archive size = %d", len(u.RecentAccesses))
	}
	u.RecentAccesses = recentAccessesForUser(&u, "")
	if u.RecentAccesses[0].Target != fmt.Sprintf("site-%04d.example", recentAccessLimit+4) {
		t.Fatalf("newest entry missing: %#v", u.RecentAccesses[0])
	}
}

func TestVersionSevenSeedsRecentAccessFromNodeStats(t *testing.T) {
	now := time.Now()
	s := &State{Version: 7, Users: []User{{Name: "alice", Nodes: []Node{{Name: "Node A", Device: "phone", Destinations: map[string]AccessStat{"example.com": {Count: 7, LastSeen: now.Format(time.RFC3339)}}}}}}}
	if err := migrateState(s); err != nil {
		t.Fatal(err)
	}
	if s.Version != stateVersion || len(s.Users[0].RecentAccesses) != 1 || s.Users[0].RecentAccesses[0].Target != "example.com" || s.Users[0].RecentAccesses[0].Count != 7 {
		t.Fatalf("recent access migration failed: %#v", s.Users[0].RecentAccesses)
	}
}
