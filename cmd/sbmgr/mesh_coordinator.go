package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sbmgr/internal/mesh"
	"slices"
	"strings"
	"time"
)

var meshExchange = func(a *app, member mesh.Member, r meshRequest, local bool) (meshResponse, error) {
	if local {
		return a.meshExecute(r)
	}
	server := meshFleetServer(member)
	if err := validateFleet(&State{Fleet: []FleetServer{server}}); err != nil {
		return meshResponse{}, errors.New("从机连接设置无效")
	}
	command := "SBMGR_HOME=" + posixShellQuote(member.AppDir) + " " + posixShellQuote(member.AppDir+"/sbmgr") + " admin mesh rpc"
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	raw, err := json.Marshal(r)
	if err != nil {
		return meshResponse{}, errors.New("无法编码节点请求")
	}
	defer clear(raw)
	cmd := exec.CommandContext(ctx, "ssh", fleetSSHArgs(server, command)...)
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(raw)
	output := newFleetLimitedBuffer(16 << 10)
	cmd.Stdout, cmd.Stderr = output, io.Discard
	if cmd.Run() != nil || output.overflow {
		return meshResponse{}, errors.New("SSH 通信失败或响应超限；检查专用密钥、主机指纹和受限命令")
	}
	var response meshResponse
	if err := decodeMeshJSON(bytes.NewReader(output.Bytes()), &response); err != nil {
		return meshResponse{}, err
	}
	if response.Protocol != mesh.Protocol || response.Member != r.Member {
		return meshResponse{}, errors.New("从机响应身份或协议不匹配")
	}
	if response.Error != "" {
		return meshResponse{}, errors.New("从机拒绝此操作；请在从机检查身份、未完成事务和本地配置")
	}
	return response, nil
}

func (a *app) meshCoordinate(operation string) error {
	s, err := a.loadCanonicalState()
	if err != nil {
		return err
	}
	if s.Mesh == nil {
		return errors.New("请在主机上执行此操作")
	}
	t := s.Mesh
	if operation == "check" {
		var failures []error
		for _, member := range t.Members {
			r := meshRequest{Protocol: mesh.Protocol, Cluster: t.ID, Member: member.ID, Operation: "status"}
			response, err := meshExchange(a, member, r, member.ID == t.Master)
			if err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", member.ID, err))
				continue
			}
			fmt.Fprintf(a.out, "%s · 管理通信正常 · 已应用修订 %d · 事务 %s\n", member.ID, response.Revision, dash(response.Phase))
		}
		return errors.Join(failures...)
	}
	if operation == "recover" {
		if s.MeshRollout == nil {
			fmt.Fprintln(a.out, "没有待恢复的主从事务")
			return nil
		}
		return a.meshFinishRollout(t, s.MeshRollout)
	}
	if s.MeshRollout != nil {
		return errors.New("存在未完成的应用事务，请先执行主从恢复")
	}
	if s.MeshAgent.Active != nil && s.MeshAgent.Active.Revision == t.Revision {
		fmt.Fprintln(a.out, "当前拓扑已生效")
		return nil
	}
	plans := map[string]mesh.Plan{}
	for _, member := range t.Members {
		plan, err := t.Compile(member.ID)
		if err != nil {
			return err
		}
		plans[member.ID] = plan
	}
	rollout := &MeshRollout{ID: "tx-" + strings.ReplaceAll(newUUID(), "-", "")[:24], Revision: t.Revision, Decision: "rollback"}
	for _, member := range t.Members {
		rollout.Members = append(rollout.Members, member.ID)
	}
	// Persist the recovery decision before any remote can prepare. All network
	// calls happen outside the state lock; local edits and statistics can proceed.
	if err := a.withStateLock(func() error {
		latest, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		if latest.Mesh == nil || latest.Mesh.Revision != t.Revision || latest.MeshRollout != nil {
			return errors.New("拓扑已变化，请刷新后重新应用")
		}
		latest.MeshRollout = rollout
		return saveState(a.statePath, latest)
	}); err != nil {
		return err
	}
	fail := func(cause error) error {
		if err := a.meshFinishRollout(t, rollout); err != nil {
			return errors.Join(cause, err)
		}
		return fmt.Errorf("%w；所有已准备节点已恢复", cause)
	}
	for _, member := range t.Members {
		plan := plans[member.ID]
		fmt.Fprintf(a.out, "正在校验节点 %s\n", member.ID)
		_, err := meshExchange(a, member, meshRequest{Protocol: mesh.Protocol, Cluster: t.ID, Member: member.ID, Operation: "prepare", Transaction: rollout.ID, Plan: &plan}, member.ID == t.Master)
		if err != nil {
			return fail(fmt.Errorf("准备节点 %s: %w", member.ID, err))
		}
	}
	// The ingress master is committed last. Every participant is prepared
	// before the first mutation. A lost response is treated as uncertain and
	// rolled back by transaction ID, including the uncertain participant.
	members := append([]mesh.Member(nil), t.Members...)
	slices.Reverse(members)
	for _, member := range members {
		if member.ID == t.Master {
			continue
		}
		if _, err := meshExchange(a, member, meshRequest{Protocol: mesh.Protocol, Cluster: t.ID, Member: member.ID, Operation: "commit", Transaction: rollout.ID}, false); err != nil {
			return fail(fmt.Errorf("应用节点 %s: %w", member.ID, err))
		}
	}
	masterIndex := slices.IndexFunc(t.Members, func(m mesh.Member) bool { return m.ID == t.Master })
	if _, err := meshExchange(a, t.Members[masterIndex], meshRequest{Protocol: mesh.Protocol, Cluster: t.ID, Member: t.Master, Operation: "commit", Transaction: rollout.ID}, true); err != nil {
		return fail(err)
	}
	if err := a.withStateLock(func() error {
		latest, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		if latest.MeshRollout == nil || latest.MeshRollout.ID != rollout.ID {
			return errors.New("主从应用日志发生变化")
		}
		latest.MeshRollout.Decision = "finalize"
		return saveState(a.statePath, latest)
	}); err != nil {
		return fail(err)
	}
	rollout.Decision = "finalize"
	return a.meshFinishRollout(t, rollout)
}

func (a *app) meshFinishRollout(t *mesh.Topology, r *MeshRollout) error {
	var failures []error
	for _, id := range r.Members {
		index := slices.IndexFunc(t.Members, func(m mesh.Member) bool { return m.ID == id })
		if index < 0 {
			return errors.New("恢复日志引用了缺失节点")
		}
		_, err := meshExchange(a, t.Members[index], meshRequest{Protocol: mesh.Protocol, Cluster: t.ID, Member: id, Operation: r.Decision, Transaction: r.ID}, id == t.Master)
		if err != nil {
			failures = append(failures, fmt.Errorf("节点 %s 恢复未完成: %w", id, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("应用日志已保留，恢复通信后执行主从恢复: %w", errors.Join(failures...))
	}
	if err := a.withStateLock(func() error {
		latest, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		if latest.MeshRollout == nil || latest.MeshRollout.ID != r.ID {
			return errors.New("恢复日志已变化")
		}
		latest.MeshRollout = nil
		return saveState(a.statePath, latest)
	}); err != nil {
		return err
	}
	if r.Decision == "finalize" {
		fmt.Fprintln(a.out, "主从拓扑已应用；可在用户节点中分配主从线路")
	} else {
		fmt.Fprintln(a.out, "主从事务已回滚")
	}
	return nil
}
