package main

import (
	"errors"
	"path/filepath"
	"sbmgr/internal/mesh"
	"slices"
)

// MeshAgentState is the durable local transaction journal. Prepared changes
// are not rendered; Previous survives until the coordinator finalizes.
type MeshAgentState struct {
	Identity        *MeshIdentity `json:"identity,omitempty"`
	Cluster         string        `json:"cluster,omitempty"`
	Member          string        `json:"member,omitempty"`
	Active          *mesh.Plan    `json:"active,omitempty"`
	Pending         *mesh.Plan    `json:"pending,omitempty"`
	Previous        *mesh.Plan    `json:"previous,omitempty"`
	Transaction     string        `json:"transaction,omitempty"`
	Phase           string        `json:"phase,omitempty"`
	LastTransaction string        `json:"last_transaction,omitempty"`
	LastOutcome     string        `json:"last_outcome,omitempty"`
}

type MeshIdentity struct {
	Master bool `json:"master"`
}

type MeshRollout struct {
	ID       string   `json:"id"`
	Revision uint64   `json:"revision"`
	Members  []string `json:"members"`
	Decision string   `json:"decision"`
}

func meshFleetServer(m mesh.Member) FleetServer {
	return normalizedFleetServer(FleetServer{Name: m.ID, Host: m.SSHHost, Port: m.SSHPort, User: m.SSHUser, KeyPath: m.SSHKeyPath, AppDir: m.AppDir})
}

func validateMeshState(s *State) error {
	if err := validateMeshAccessState(s); err != nil {
		return err
	}
	if s.Mesh != nil {
		if err := s.Mesh.Validate(); err != nil {
			return err
		}
		for _, m := range s.Mesh.Members {
			if m.ID == s.Mesh.Master {
				continue
			}
			if err := validateFleet(&State{Fleet: []FleetServer{meshFleetServer(m)}}); err != nil {
				return errors.New("从机 SSH 接入参数无效")
			}
			if m.AppDir == "" || !filepath.IsAbs(m.SSHKeyPath) {
				return errors.New("从机需要明确的应用目录和 SSH 私钥绝对路径")
			}
		}
	}
	a := s.MeshAgent
	if a.Cluster == "" && a.Member == "" && a.Identity == nil && a.Active == nil && a.Pending == nil && a.Previous == nil && a.Transaction == "" && a.Phase == "" && s.Mesh == nil && s.MeshRollout == nil {
		return nil
	}
	if !mesh.ValidID(a.Cluster) || !mesh.ValidID(a.Member) {
		return errors.New("本机主从身份无效")
	}
	if s.Mesh != nil && (s.Mesh.Master != a.Member || s.Mesh.ID != a.Cluster) {
		return errors.New("主机拓扑与本机身份不符")
	}
	if identity := a.Identity; identity != nil {
		if identity.Master != (s.Mesh != nil) {
			return errors.New("接入身份与管理角色不符")
		}
	}
	for _, p := range []*mesh.Plan{a.Active, a.Pending, a.Previous} {
		if p == nil {
			continue
		}
		if err := p.Validate(); err != nil {
			return err
		}
		if p.Cluster != a.Cluster || p.Member != a.Member {
			return errors.New("节点配置不属于本机")
		}
		if (s.Mesh != nil) != p.Master {
			return errors.New("节点执行角色与本机身份不符")
		}
		if i := a.Identity; i != nil && i.Master != p.Master {
			return errors.New("节点计划与批准身份不符")
		}
	}
	if a.Transaction != "" {
		if !mesh.ValidID(a.Transaction) || a.Pending == nil || (a.Phase != "prepared" && a.Phase != "committing" && a.Phase != "committed") {
			return errors.New("主从应用事务不完整")
		}
		if a.Previous != nil && a.Pending.Revision <= a.Previous.Revision {
			return errors.New("主从候选修订未向前推进")
		}
		if a.Phase == "committed" && (a.Active == nil || a.Active.Revision != a.Pending.Revision) {
			return errors.New("主从提交记录与生效修订不符")
		}
	} else if a.Pending != nil || a.Previous != nil || a.Phase != "" {
		return errors.New("主从事务缺少标识")
	}
	if a.LastTransaction != "" && (!mesh.ValidID(a.LastTransaction) || (a.LastOutcome != "finalized" && a.LastOutcome != "rolled-back")) {
		return errors.New("主从完成记录无效")
	}
	if s.MeshRollout != nil {
		r := s.MeshRollout
		if s.Mesh == nil || !mesh.ValidID(r.ID) || r.Revision != s.Mesh.Revision || len(r.Members) != len(s.Mesh.Members) || (r.Decision != "rollback" && r.Decision != "finalize") {
			return errors.New("主机应用日志无效")
		}
		seen := map[string]bool{}
		for _, id := range r.Members {
			if seen[id] || !slices.ContainsFunc(s.Mesh.Members, func(m mesh.Member) bool { return m.ID == id }) {
				return errors.New("主机应用日志节点无效或重复")
			}
			seen[id] = true
		}
	}
	return nil
}
