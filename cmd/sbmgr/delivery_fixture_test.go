package main

import "strings"

func sampleDeliveryState() *State {
	return &State{Subscription: SubscriptionSettings{Enabled: true, Listen: "127.0.0.1:18080"}, Users: []User{{
		Name: "alice", Enabled: true,
		Devices: []Device{{Name: "phone", Enabled: true, SubscriptionToken: strings.Repeat("a", 32)}},
		Nodes:   []Node{{Name: "Node A", Device: "phone"}},
	}}}
}
