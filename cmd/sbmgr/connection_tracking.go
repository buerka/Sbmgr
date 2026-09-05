package main

import (
	"container/heap"
	"sort"
)

const activeConnectionLimit = 4096

type connectionEntry struct{ id, seen string }
type connectionHeap struct {
	entries []connectionEntry
	indices map[string]int
}

func (h connectionHeap) Len() int           { return len(h.entries) }
func (h connectionHeap) Less(i, j int) bool { return h.entries[i].seen < h.entries[j].seen }
func (h connectionHeap) Swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.indices[h.entries[i].id] = i
	h.indices[h.entries[j].id] = j
}
func (h *connectionHeap) Push(v any) {
	e := v.(connectionEntry)
	h.indices[e.id] = len(h.entries)
	h.entries = append(h.entries, e)
}
func (h *connectionHeap) Pop() any {
	i := len(h.entries) - 1
	e := h.entries[i]
	h.entries = h.entries[:i]
	delete(h.indices, e.id)
	return e
}

func trackActiveConnection(s *State, c ActiveConnection) {
	if c.ID == "" {
		return
	}
	if s.ActiveConnections == nil {
		s.ActiveConnections = map[string]ActiveConnection{}
		s.connectionIndex = nil
	}
	if s.connectionIndex == nil {
		h := &connectionHeap{indices: map[string]int{}}
		for id, c := range s.ActiveConnections {
			heap.Push(h, connectionEntry{id, c.LastSeen})
		}
		s.connectionIndex = h
	}
	h := s.connectionIndex
	if i, exists := h.indices[c.ID]; exists {
		h.entries[i].seen = c.LastSeen
		heap.Fix(h, i)
	} else {
		heap.Push(h, connectionEntry{c.ID, c.LastSeen})
	}
	s.ActiveConnections[c.ID] = c
	for h.Len() > activeConnectionLimit {
		e := heap.Pop(h).(connectionEntry)
		delete(s.ActiveConnections, e.id)
	}
}

func boundConnectionTracking(s *State) {
	if len(s.ActiveConnections) <= activeConnectionLimit {
		return
	}
	entries := make([]ActiveConnection, 0, len(s.ActiveConnections))
	for id, c := range s.ActiveConnections {
		c.ID = id
		entries = append(entries, c)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].LastSeen == entries[j].LastSeen {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].LastSeen > entries[j].LastSeen
	})
	s.ActiveConnections = make(map[string]ActiveConnection, activeConnectionLimit)
	for _, c := range entries[:activeConnectionLimit] {
		s.ActiveConnections[c.ID] = c
	}
	s.connectionIndex = nil
}
