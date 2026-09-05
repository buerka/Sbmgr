package main

import "fmt"

// Fixed mark keys are authoritative. During upgrade, a legacy display label
// is accepted only if it identifies exactly one node; ambiguous counters are
// never assigned to another user's quota. Real nft JSON also supplies marks.
func canonicalNftCounters(s *State, counters map[string]int64) map[string]int64 {
	result := make(map[string]int64, len(counters))
	for k, v := range counters {
		result[k] = v
	}
	owners := map[string]int{}
	marks := map[string]uint32{}
	for _, u := range s.Users {
		for _, n := range u.Nodes {
			label := deviceNodeLabel(u.Name, n.Device, n.Name)
			owners[label]++
			marks[label] = n.RateMark
		}
	}
	for label, count := range owners {
		if count == 1 {
			for _, direction := range []string{"upload", "download"} {
				key := fmt.Sprintf("sbmgr:%08x %s", marks[label], direction)
				if _, exists := result[key]; exists {
					continue
				}
				if v, exists := counters[label+" "+direction]; exists {
					result[key] = v
				}
			}
		}
	}
	return result
}
