package main

import "errors"

type portalSiteBlockInput struct {
	Action   string `json:"action"`
	Domain   string `json:"domain"`
	Expected string `json:"expected"`
}

func portalSiteBlocksReply(u *User, pending bool, message string) webReply {
	domains := append([]string{}, u.PersonalBlockedDomains...)
	return webJSON(200, map[string]any{
		"domains": domains, "version": personalSiteBlockVersion(u),
		"pending": pending, "limit": personalSiteBlockLimit, "message": message,
	})
}

// The privileged backend has already checked the session, origin and CSRF.
// Called under the cross-process state lock by lookupPortalLocked.
func (b *webBackend) changePortalSiteBlocksLocked(a *app, s *State, u *User, q webRequest) webReply {
	if b.busy {
		return webError(409, "管理员正在保存设置，请稍后重试")
	}
	var input portalSiteBlockInput
	if webDecode(q.Body, &input) != nil || len(input.Expected) != 64 || len(input.Domain) > 2048 {
		return webError(400, "网站操作格式不正确")
	}
	if s.MeshRollout != nil || s.MeshAgent.Transaction != "" || (s.Mesh != nil && (s.MeshAgent.Active == nil || s.MeshAgent.Active.Revision != s.Mesh.Revision)) {
		return webError(409, "线路正在调整，请稍后重试")
	}
	if configurationPending(s) || runtimeApplyPending(s) {
		return webError(409, "上一项配置仍在应用中，请稍后刷新；持续失败请联系管理员")
	}
	changed, err := changePersonalSiteBlock(u, input.Action, input.Domain, input.Expected)
	if err != nil {
		if input.Expected != personalSiteBlockVersion(u) {
			return webError(409, "网站列表已更新，请刷新后重试")
		}
		return webError(400, err.Error())
	}
	if !changed {
		return portalSiteBlocksReply(u, s.StatsApplyPending, "网站列表没有变化。")
	}
	s.StatsApplyPending = true
	err = a.withAuditedStateLock("portal.site-block."+input.Action, nil, func() error {
		if err := saveState(a.statePath, s); err != nil {
			return errors.New("网站限制保存失败，原设置保持不变")
		}
		return nil
	})
	if err != nil {
		return webError(500, "网站限制保存失败，原设置保持不变")
	}
	return portalSiteBlocksReply(u, true, "网站限制已保存，将在各入口自动应用，通常一分钟内生效。")
}
