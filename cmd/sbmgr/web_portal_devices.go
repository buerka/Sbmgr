package main

import (
	"encoding/json"
	"errors"
	"time"
)

// Runs under the account revalidation and cross-process lock in lookupPortalLocked.
// The owner always comes from the authenticated session, never from the body.
func (b *webBackend) changePortalDeviceLocked(a *app, s *State, u *User, q webRequest, now time.Time) webReply {
	if b.busy {
		return webError(409, "管理员正在保存设置，请稍后重试")
	}
	var input portalDeviceInput
	if webDecode(q.Body, &input) != nil || len(input.Expected) != 64 || len(input.Name) > 256 || len(input.Device) > 256 || len(input.From) > 256 {
		return webError(400, "设备操作格式不正确")
	}
	if s.MeshRollout != nil || s.MeshAgent.Transaction != "" || (s.Mesh != nil && (s.MeshAgent.Active == nil || s.MeshAgent.Active.Revision != s.Mesh.Revision)) {
		return webError(409, "线路正在调整，请稍后重试")
	}
	if input.Action == "rotate-link" {
		if err := requireDeviceCredentialRotationReady(s); err != nil {
			return webError(409, err.Error())
		}
	}
	if (input.Action == "add" || input.Action == "delete") && (configurationPending(s) || runtimeApplyPending(s)) {
		return webError(409, "上一项配置仍在应用中，请稍后刷新；持续失败请联系管理员")
	}
	// Build a detached candidate so a failed validation/save cannot mutate the
	// account object or leave behind a partial grant in a long-lived process.
	raw, err := json.Marshal(s)
	if err != nil {
		return webError(500, "读取设备设置失败")
	}
	var next State
	if json.Unmarshal(raw, &next) != nil {
		return webError(500, "读取设备设置失败")
	}
	target := findUser(&next, u.Name)
	if err := changePersonalDevice(&next, target, input, now); err != nil {
		return webError(400, err.Error())
	}
	if input.Action == "add" || input.Action == "delete" {
		next.StatsApplyPending = true
	}
	err = a.withAuditedStateLock("portal.device."+input.Action, nil, func() error {
		if err := saveState(a.statePath, &next); err != nil {
			return errors.New("设备保存失败，原配置保持不变")
		}
		return nil
	})
	if err != nil {
		return webError(500, "设备保存失败，原配置保持不变")
	}
	message := "设备名称已更新，订阅地址与节点身份保持不变。"
	if input.Action == "add" {
		message = "设备已创建，独立订阅已生成；入口将自动应用授权，通常一分钟内生效。"
	}
	if input.Action == "delete" {
		message = "设备已删除，订阅已失效；入口将自动撤销授权，通常一分钟内生效。已计入用户的用量保留。"
	}
	if input.Action == "rotate-link" {
		message = "设备订阅及连接凭据已重置，旧订阅立即失效；各入口正在等待自动应用，旧配置将在应用后失效。请重新获取并导入订阅。"
	}
	return webJSON(200, map[string]any{"message": message, "pending": next.StatsApplyPending})
}
