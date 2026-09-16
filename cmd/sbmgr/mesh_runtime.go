package main

import (
	"errors"
	"io"
	"sbmgr/internal/mesh"
)

var meshCheckCandidate = func(s *State) error {
	if err := checkRendered(s, io.Discard); err != nil {
		return errors.New("节点 sing-box 候选校验失败，现有配置未变更")
	}
	if err := checkRateLimits(s, io.Discard); err != nil {
		return errors.New("节点 nftables 候选校验失败，现有配置未变更")
	}
	return nil
}
var meshApplyCandidate = func(a *app, s *State, target, previous *mesh.Plan) error {
	if err := applyState(s, false, true, io.Discard); err != nil {
		old := *s
		old.MeshAgent.Active = previous
		if err := applyState(&old, false, true, io.Discard); err != nil {
			return errors.New("节点应用失败且回滚未完成；保留事务，请执行主从恢复")
		}
		return errors.New("节点应用失败，已恢复配置")
	}
	return nil
}
