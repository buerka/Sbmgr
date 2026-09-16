package main

import (
	"errors"
	"net"
	"sbmgr/internal/mesh"
	"slices"
	"strconv"
	"strings"
)

func setMeshRoute(t *mesh.Topology, id string, path []string, protocolList, endpointList string, rotate bool) error {
	return setMeshRouteOptions(t, id, path, protocolList, endpointList, rotate, "", "")
}

func setMeshRouteOptions(t *mesh.Topology, id string, path []string, protocolList, endpointList string, rotate bool, entry, exit string) error {
	if !mesh.ValidID(id) || len(path) < 1 || len(path) > mesh.MaxHops {
		return errors.New("线路标识无效，或跳数不在 1–16 范围")
	}
	index := slices.IndexFunc(t.Routes, func(r mesh.Route) bool { return r.ID == id })
	old := mesh.Route{}
	if index >= 0 {
		old = t.Routes[index]
	}
	if entry == "" {
		entry = t.Master
	}
	route := mesh.Route{ID: id, Hops: path, Entry: entry, Exit: exit}
	if len(path) != 1 || path[0] != entry {
		protocols, endpoints := []string{}, []string{}
		if protocolList != "" {
			protocols = strings.Split(protocolList, ",")
		}
		if endpointList != "" {
			endpoints = strings.Split(endpointList, ",")
		}
		if (len(protocols) != 0 && len(protocols) != 1 && len(protocols) != len(path)) || (len(endpoints) != 0 && len(endpoints) != len(path)) {
			return errors.New("协议需填写一个统一值或逐跳填写；数据地址需与跳数一致")
		}
		for i, memberID := range path {
			memberIndex := slices.IndexFunc(t.Members, func(m mesh.Member) bool { return m.ID == memberID })
			if memberIndex < 0 || memberID == entry {
				return errors.New("中转链包含不存在的从机")
			}
			previous := mesh.Transport{}
			if j := slices.Index(old.Hops, memberID); j >= 0 && j < len(old.Transports) {
				previous = old.Transports[j]
			}
			kind := previous.Type
			if kind == "" {
				kind = "hysteria2"
			}
			if len(protocols) == 1 {
				kind = mesh.NormalizeTransport(protocols[0])
			} else if len(protocols) > 1 {
				kind = mesh.NormalizeTransport(protocols[i])
			}
			host, port := previous.Server, previous.Port
			if host == "" {
				host = t.Members[memberIndex].SSHHost
				if host == "" && t.Members[memberIndex].Client != nil {
					host = t.Members[memberIndex].Client.Server
				}
			}
			if port == 0 {
				used := map[int]bool{}
				for _, r := range t.Routes {
					if r.ID == id {
						continue
					}
					for j, m := range r.Hops {
						if m == memberID && j < len(r.Transports) {
							used[r.Transports[j].Port] = true
						}
					}
				}
				port = 20000
				for used[port] {
					port++
				}
			}
			if len(endpoints) > 0 {
				endpoint := strings.TrimSpace(endpoints[i])
				if err := mesh.ValidateEndpoint(endpoint); err != nil {
					return err
				}
				host, endpointPort, _ := net.SplitHostPort(endpoint)
				parsed, _ := strconv.Atoi(endpointPort)
				// The data address can be entirely different from the SSH address.
				previous.Server, previous.Port = host, parsed
			} else {
				previous.Server, previous.Port = host, port
			}
			if previous.Type != kind || rotate {
				var err error
				previous, err = mesh.NewTransport(kind, previous.Server, previous.Port)
				if err != nil {
					return err
				}
			}
			route.Transports = append(route.Transports, previous)
		}
	}
	candidate := *t
	candidate.Routes = slices.Clone(t.Routes)
	if index < 0 {
		candidate.Routes = append(candidate.Routes, route)
	} else {
		candidate.Routes[index] = route
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	t.Routes = candidate.Routes
	return nil
}
