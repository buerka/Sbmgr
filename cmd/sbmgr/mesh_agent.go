package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sbmgr/internal/mesh"
	"time"
)

type meshRequest struct {
	Protocol    int        `json:"protocol"`
	Cluster     string     `json:"cluster"`
	Member      string     `json:"member"`
	Operation   string     `json:"operation"`
	Transaction string     `json:"transaction,omitempty"`
	Plan        *mesh.Plan `json:"plan,omitempty"`
}

type meshResponse struct {
	Protocol    int    `json:"protocol"`
	Member      string `json:"member"`
	Revision    uint64 `json:"revision"`
	Phase       string `json:"phase,omitempty"`
	Transaction string `json:"transaction,omitempty"`
	Error       string `json:"error,omitempty"`
}

func decodeMeshJSON(reader io.Reader, target any) error {
	raw, err := io.ReadAll(io.LimitReader(reader, mesh.MaxMessage+1))
	if err != nil || len(raw) > mesh.MaxMessage {
		return errors.New("节点报文读取失败或超过上限")
	}
	defer clear(raw)
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return errors.New("节点报文格式无效")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("节点报文含多余内容")
	}
	return nil
}

func (a *app) meshAgentRPC(reader io.Reader) error {
	var request meshRequest
	if err := decodeMeshJSON(reader, &request); err != nil {
		return err
	}
	response, err := a.meshExecute(request)
	if err != nil {
		response.Error = err.Error()
	}
	return json.NewEncoder(a.out).Encode(response)
}

func (a *app) meshExecute(r meshRequest) (meshResponse, error) {
	response := meshResponse{Protocol: mesh.Protocol}
	err := a.withStateLock(func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return errors.New("节点状态不可用")
		}
		j := &s.MeshAgent
		if r.Protocol != mesh.Protocol || r.Cluster != j.Cluster || r.Member != j.Member || j.Member == "" {
			return errors.New("节点身份或协议不匹配，请先在从机导入接入配置")
		}
		response.Member = j.Member
		if j.Active != nil {
			response.Revision = j.Active.Revision
		}
		response.Phase, response.Transaction = j.Phase, j.Transaction
		if r.Operation == "status" {
			if r.Plan != nil || r.Transaction != "" {
				return errors.New("状态请求含无效参数")
			}
			return nil
		}
		if !mesh.ValidID(r.Transaction) {
			return errors.New("应用事务标识无效")
		}
		if r.Operation != "prepare" && r.Plan != nil {
			return errors.New("此操作不接受候选配置")
		}
		if j.Transaction != "" && j.Transaction != r.Transaction {
			return errors.New("节点有未完成事务，请先恢复")
		}
		if j.Transaction == "" && j.LastTransaction == r.Transaction {
			if (r.Operation == "finalize" && j.LastOutcome == "finalized") || (r.Operation == "rollback" && j.LastOutcome == "rolled-back") {
				return nil
			}
			return errors.New("事务已经结束")
		}
		switch r.Operation {
		case "prepare":
			if r.Plan == nil {
				return errors.New("缺少节点候选")
			}
			if err := r.Plan.Validate(); err != nil {
				return err
			}
			if r.Plan.Cluster != j.Cluster || r.Plan.Member != j.Member {
				return errors.New("候选不属于本节点")
			}
			if (s.Mesh != nil) != r.Plan.Master {
				return errors.New("候选执行角色不匹配")
			}
			if identity := j.Identity; identity != nil && r.Plan.Master != identity.Master {
				return errors.New("候选与本机批准的接入身份不符")
			}
			if j.Pending != nil {
				old, _ := json.Marshal(j.Pending)
				next, _ := json.Marshal(r.Plan)
				if !bytes.Equal(old, next) {
					return errors.New("事务候选发生变化")
				}
				return nil
			}
			if j.Active != nil {
				if r.Plan.Revision <= j.Active.Revision {
					return errors.New("候选修订号必须高于已生效版本")
				}
			}
			candidate := *s
			candidate.MeshAgent.Active = r.Plan
			if err := meshCheckCandidate(&candidate); err != nil {
				return err
			}
			if _, err := createManualStateBackup(a.statePath, time.Now()); err != nil {
				return errors.New("准备前状态备份失败")
			}
			j.Pending, j.Previous, j.Transaction, j.Phase = r.Plan, j.Active, r.Transaction, "prepared"
		case "commit":
			if j.Transaction != r.Transaction || j.Pending == nil {
				return errors.New("节点尚未准备此事务")
			}
			if j.Phase == "committed" {
				return nil
			}
			j.Phase = "committing"
			if err := saveState(a.statePath, s); err != nil {
				return errors.New("无法持久保存应用日志")
			}
			j.Active = j.Pending
			if err := meshApplyCandidate(a, s, j.Active, j.Previous); err != nil {
				return err
			}
			j.Phase = "committed"
		case "rollback":
			if j.Transaction == "" {
				return nil
			}
			if j.Phase == "committing" || j.Phase == "committed" {
				current := j.Active
				if current == nil {
					current = j.Pending
				}
				j.Active = j.Previous
				if err := meshApplyCandidate(a, s, j.Previous, current); err != nil {
					return err
				}
			}
			j.Pending, j.Previous, j.Transaction, j.Phase = nil, nil, "", ""
			j.LastTransaction, j.LastOutcome = r.Transaction, "rolled-back"
		case "finalize":
			if j.Transaction != r.Transaction || j.Phase != "committed" {
				return errors.New("节点尚未提交此事务")
			}
			j.Pending, j.Previous, j.Transaction, j.Phase = nil, nil, "", ""
			j.LastTransaction, j.LastOutcome = r.Transaction, "finalized"
		default:
			return errors.New("不支持的节点操作")
		}
		if err := saveState(a.statePath, s); err != nil {
			return errors.New("节点事务保存失败，请恢复未完成事务")
		}
		_ = appendAuditRecord(a.statePath, AuditRecord{At: time.Now().Format(time.RFC3339Nano), Actor: auditActor(), Action: "mesh." + r.Operation})
		response.Phase, response.Transaction = j.Phase, j.Transaction
		if j.Active != nil {
			response.Revision = j.Active.Revision
		}
		return nil
	})
	return response, err
}
