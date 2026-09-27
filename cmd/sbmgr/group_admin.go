package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
)

type groupChange struct {
	ID        string      `json:"id"`
	Name      string      `json:"name,omitempty"`
	Policy    GroupPolicy `json:"policy"`
	Users     []string    `json:"users,omitempty"`
	Mode      string      `json:"mode,omitempty"`
	Overrides []string    `json:"overrides,omitempty"`
	Expected  string      `json:"expected,omitempty"`
}

func (a *app) groupChange(kind string, input groupChange, requireVersion bool) error {
	return a.withAuditedStateLock("group."+kind, nil, func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		if s.Mesh == nil && s.MeshAgent.Cluster != "" {
			return errors.New("请在主机统一管理用户分组")
		}
		if (requireVersion || input.Expected != "") && input.Expected != userGroupVersion(s) {
			return errors.New("分组或成员设置已变化，请刷新后重试；没有保存任何修改")
		}
		if err := applyGroupChange(s, kind, input); err != nil {
			return err
		}
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "分组设置已原子保存；应用配置后更新运行中的入口策略。个人覆盖项与累计用量保持不变。")
		return nil
	})
}

// The caller owns the state lock and commits only after the whole operation
// succeeds. Partial member changes remain in memory if any validation fails.
func applyGroupChange(s *State, kind string, input groupChange) error {
	g := findGroup(s, input.ID)
	switch kind {
	case "save":
		if err := validateGroupPolicy(input.Policy); err != nil {
			return err
		}
		if input.Policy.Routes != nil && (g == nil || !reflect.DeepEqual(g.Policy.Routes, input.Policy.Routes)) {
			if _, _, err := groupRouteSelection(s, *input.Policy.Routes); err != nil {
				return err
			}
		}
		name := strings.TrimSpace(input.Name)
		if err := validateManagedName(name); err != nil {
			return errors.New("请输入有效的分组名称")
		}
		for _, other := range s.UserGroups {
			if other.ID != input.ID && strings.EqualFold(other.Name, name) {
				return errors.New("分组名称已存在")
			}
		}
		if input.ID == "" {
			if len(s.UserGroups) >= 256 {
				return errors.New("分组数量超过上限")
			}
			s.UserGroups = append(s.UserGroups, UserGroup{ID: "g-" + newUUID(), Name: name, Policy: input.Policy})
			return nil
		}
		if g == nil {
			return errors.New("分组不存在")
		}
		changed := []string{}
		for _, key := range groupScopes {
			if !reflect.DeepEqual(groupValue(g.Policy, key), groupValue(input.Policy, key)) {
				changed = append(changed, key)
			}
		}
		g.Name, g.Policy = name, input.Policy
		for i := range s.Users {
			if s.Users[i].GroupID == g.ID {
				if err := applyGroupPolicy(s, &s.Users[i], changed); err != nil {
					return err
				}
			}
		}
	case "members":
		if g == nil {
			return errors.New("目标分组不存在")
		}
		if len(input.Users) == 0 || len(input.Users) > 1000 {
			return errors.New("请选择 1–1000 位用户")
		}
		var overrides []string
		switch input.Mode {
		case "preserve":
			overrides = append([]string(nil), groupScopes...)
		case "inherit":
		case "custom":
			for _, key := range input.Overrides {
				if !slices.Contains(groupScopes, key) || slices.Contains(overrides, key) {
					return errors.New("个人覆盖项无效")
				}
				overrides = append(overrides, key)
			}
		default:
			return errors.New("请选择保留个人配置或继承分组规则")
		}
		seen := map[string]bool{}
		for _, name := range input.Users {
			u := findUser(s, name)
			if u == nil || seen[name] {
				return errors.New("成员不存在或重复；所有修改已取消")
			}
			seen[name] = true
			u.GroupID = input.ID
			u.GroupOverrides = append([]string(nil), overrides...)
			if err := applyGroupPolicy(s, u, groupScopes); err != nil {
				return err
			}
		}
	case "delete":
		if g == nil {
			return errors.New("分组不存在")
		}
		if g.ID == defaultGroupID {
			return errors.New("默认分组不能删除")
		}
		for _, u := range s.Users {
			if u.GroupID == g.ID {
				return errors.New("请先将全部成员移出分组，再删除空分组")
			}
		}
		s.UserGroups = slices.DeleteFunc(s.UserGroups, func(v UserGroup) bool { return v.ID == input.ID })
	default:
		return errors.New("未知分组操作")
	}
	return nil
}

func (a *app) groupCmd(args []string) error {
	if len(args) == 1 && args[0] == "list" {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		users := []any{}
		for _, u := range s.Users {
			users = append(users, map[string]any{"name": u.Name, "group_id": u.GroupID, "overrides": u.GroupOverrides})
		}
		return json.NewEncoder(a.out).Encode(map[string]any{"groups": s.UserGroups, "members": users, "version": userGroupVersion(s)})
	}
	if len(args) == 0 || !slices.Contains([]string{"save", "members", "delete"}, args[0]) {
		return errors.New("用法: admin group list | save|members|delete --file REQUEST.json")
	}
	fs := a.newFlagSet("group " + args[0])
	file := fs.String("file", "", "操作 JSON 文件；expected 可用于并发校验")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || *file == "" {
		return errors.New("必须指定 --file")
	}
	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, webMaxRequest+1))
	if err != nil {
		return err
	}
	var input groupChange
	if webDecode(data, &input) != nil {
		return errors.New("分组操作 JSON 格式无效")
	}
	return a.groupChange(args[0], input, false)
}
