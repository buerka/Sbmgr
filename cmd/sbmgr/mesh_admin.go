package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sbmgr/internal/mesh"
	"slices"
	"strings"
)

func (a *app) meshCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: sbmgr admin mesh init|add|route|remove-route|remove|export|join|check|apply|recover|list")
	}
	switch args[0] {
	case "rpc":
		if len(args) != 1 {
			return errors.New("rpc 不接受参数")
		}
		return a.meshAgentRPC(os.Stdin)
	case "apply", "recover", "check":
		if len(args) != 1 {
			return errors.New("主从操作不接受额外参数")
		}
		return a.meshCoordinate(args[0])
	}
	return a.withAuditedStateLock(auditAction("mesh", args), args, func() error {
		s, err := loadState(a.statePath)
		bootstrap := false
		if err != nil {
			_, stateErr := os.Stat(a.statePath)
			_, legacyErr := os.Stat(filepath.Join(filepath.Dir(a.statePath), "state.json"))
			if args[0] != "join" || !errors.Is(stateErr, os.ErrNotExist) || !errors.Is(legacyErr, os.ErrNotExist) {
				return err
			}
			s = &State{Version: stateVersion, BaseConfig: filepath.Join(filepath.Dir(a.statePath), "config.base.json"), ConfigPath: filepath.Join(filepath.Dir(a.statePath), "sing-box.json"), InboundTag: "mesh-local", SingBoxBin: "sing-box", Service: "sing-box"}
			bootstrap = true
		}
		if args[0] == "list" {
			if s.Mesh == nil {
				fmt.Fprintln(a.out, "本机尚未配置主机拓扑")
				return nil
			}
			for _, m := range s.Mesh.Members {
				fmt.Fprintf(a.out, "节点 %s\t管理连接 %s\n", m.ID, m.SSHHost)
			}
			for _, r := range s.Mesh.Routes {
				fmt.Fprintf(a.out, "线路 %s\t%s → 就地落地\n", r.ID, strings.Join(r.Hops, " → "))
			}
			return nil
		}
		if args[0] == "export" {
			if s.Mesh == nil {
				return errors.New("请先初始化主机")
			}
			fs := a.newFlagSet("mesh export")
			id, output := fs.String("node", "", "节点标识"), fs.String("output", "", "接入文件绝对路径")
			if err := fs.Parse(args[1:]); err != nil {
				return err
			}
			if fs.NArg() != 0 || !filepath.IsAbs(*output) || *id == s.Mesh.Master {
				return errors.New("需要从机标识与输出文件绝对路径")
			}
			plan, err := s.Mesh.Compile(*id)
			if err != nil {
				return err
			}
			plan.Hops = nil // Enrollment conveys management identity only.
			raw, _ := json.MarshalIndent(plan, "", "  ")
			defer clear(raw)
			if err := writeMeshExport(*output, raw); err != nil {
				return err
			}
			fmt.Fprintln(a.out, "接入文件已保存；在从机导入并配置专用 SSH 管理密钥后可测试通信")
			return nil
		}
		if s.MeshRollout != nil || s.MeshAgent.Transaction != "" {
			return errors.New("有未完成的主从应用事务，请先恢复")
		}
		if args[0] == "join" {
			fs := a.newFlagSet("mesh join")
			file := fs.String("file", "", "接入文件绝对路径")
			if err := fs.Parse(args[1:]); err != nil {
				return err
			}
			if fs.NArg() != 0 || !filepath.IsAbs(*file) {
				return errors.New("需要接入文件绝对路径")
			}
			if s.Mesh != nil || s.MeshAgent.Cluster != "" {
				return errors.New("本机已加入主从集合，拒绝覆盖身份")
			}
			f, err := os.Open(*file)
			if err != nil {
				return errors.New("无法读取接入文件")
			}
			defer f.Close()
			var plan mesh.Plan
			if err := decodeMeshJSON(f, &plan); err != nil {
				return err
			}
			if err := plan.Validate(); err != nil {
				return err
			}
			if plan.Master {
				return errors.New("只能导入从机接入配置")
			}
			s.MeshAgent = MeshAgentState{Cluster: plan.Cluster, Member: plan.Member}
			if len(plan.Hops) != 0 {
				return errors.New("接入文件只能包含管理身份；线路通过主机单独应用")
			}
			s.MeshAgent.Identity = &MeshIdentity{Master: false}
		} else if args[0] == "init" {
			if s.Mesh != nil || s.MeshAgent.Cluster != "" {
				return errors.New("本机已配置主从身份")
			}
			fs := a.newFlagSet("mesh init")
			id, cluster := fs.String("id", "master", "本机标识"), fs.String("cluster", "default", "主从集合标识")
			if err := fs.Parse(args[1:]); err != nil {
				return err
			}
			if fs.NArg() != 0 {
				return errors.New("init 不接受位置参数")
			}
			s.Mesh = &mesh.Topology{ID: *cluster, Master: *id, Revision: 1, Members: []mesh.Member{{ID: *id}}}
			s.MeshAgent = MeshAgentState{Cluster: *cluster, Member: *id, Identity: &MeshIdentity{Master: true}}
		} else {
			if s.Mesh == nil {
				return errors.New("此操作需要在主机上执行")
			}
			fs := a.newFlagSet("mesh " + args[0])
			id := fs.String("id", "", "节点或线路标识")
			switch args[0] {
			case "add":
				host := fs.String("host", "", "SSH 管理地址")
				port, user := fs.Int("port", 22, "SSH 端口"), fs.String("user", "root", "SSH 用户")
				key, home := fs.String("key", "", "专用 SSH 私钥路径"), fs.String("home", "", "远端应用目录")
				if err := fs.Parse(args[1:]); err != nil {
					return err
				}
				if len(s.Mesh.Members) >= mesh.MaxMembers {
					return errors.New("节点数已达上限")
				}
				s.Mesh.Members = append(s.Mesh.Members, mesh.Member{ID: *id, SSHHost: *host, SSHPort: *port, SSHUser: *user, SSHKeyPath: *key, AppDir: *home})
			case "route":
				hops := fs.String("hops", "", "节点标识，以逗号分隔；最后一跳就地落地")
				protocols := fs.String("protocols", "", "逐跳协议或统一协议：socks、hysteria2、wireguard；新线路默认 hysteria2")
				endpoints := fs.String("endpoints", "", "逐跳数据地址 host:port；留空使用节点 SSH 主机名和自动端口")
				rotate := fs.Bool("rotate", false, "重新生成线路认证材料与证书；应用后生效")
				if err := fs.Parse(args[1:]); err != nil {
					return err
				}
				path := strings.Split(*hops, ",")
				for i := range path {
					path[i] = strings.TrimSpace(path[i])
				}
				if err := setMeshRoute(s.Mesh, *id, path, *protocols, *endpoints, *rotate); err != nil {
					return err
				}
			case "remove-route":
				if err := fs.Parse(args[1:]); err != nil {
					return err
				}
				for _, u := range s.Users {
					for _, n := range u.Nodes {
						if n.Outbound == mesh.RouteTag(*id) {
							return errors.New("线路仍被用户节点引用，请先更换用户线路")
						}
					}
				}
				if !slices.ContainsFunc(s.Mesh.Routes, func(r mesh.Route) bool { return r.ID == *id }) {
					return errors.New("线路不存在")
				}
				s.Mesh.Routes = slices.DeleteFunc(s.Mesh.Routes, func(r mesh.Route) bool { return r.ID == *id })
			case "remove":
				if err := fs.Parse(args[1:]); err != nil {
					return err
				}
				if *id == s.Mesh.Master {
					return errors.New("不能移除主机")
				}
				if active := s.MeshAgent.Active; active != nil && active.Revision != s.Mesh.Revision {
					return errors.New("请先应用当前拓扑以停用从机上的旧线路，再移除从机")
				}
				for _, r := range s.Mesh.Routes {
					if slices.Contains(r.Hops, *id) {
						return errors.New("节点仍被线路引用，请先修改线路")
					}
				}
				if !slices.ContainsFunc(s.Mesh.Members, func(m mesh.Member) bool { return m.ID == *id }) {
					return errors.New("节点不存在")
				}
				s.Mesh.Members = slices.DeleteFunc(s.Mesh.Members, func(m mesh.Member) bool { return m.ID == *id })
			default:
				return errors.New("未知主从操作")
			}
			if fs.NArg() != 0 {
				return errors.New("主从操作不接受位置参数")
			}
			s.Mesh.Revision++
		}
		if err := validateMeshState(s); err != nil {
			return err
		}
		if bootstrap {
			if _, err := os.Stat(s.ConfigPath); !errors.Is(err, os.ErrNotExist) {
				return errors.New("运行配置已存在，拒绝覆盖；请先初始化现有配置")
			}
			base := []byte(`{"inbounds":[{"type":"vless","tag":"mesh-local","listen":"127.0.0.1","listen_port":19099,"users":[]}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`)
			if err := writeMeshExport(s.BaseConfig, base); err != nil {
				return err
			}
		}
		if err := saveState(a.statePath, s); err != nil {
			if bootstrap {
				_ = os.Remove(s.BaseConfig)
			}
			return err
		}
		fmt.Fprintln(a.out, "已保存主从设置；完成节点接入后，在主从页面应用拓扑生效")
		return nil
	})
}

func writeMeshExport(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("无法创建接入文件目录")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("无法创建接入文件；为保护既有资料，输出路径必须不存在")
	}
	_, writeErr := f.Write(append(raw, '\n'))
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		_ = os.Remove(path)
		return errors.New("接入文件保存失败")
	}
	return nil
}
