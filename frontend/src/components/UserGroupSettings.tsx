import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import type { Snapshot, User } from "../types";
import { groupName, groupsOf, groupScopes, groupSummary } from "./groupModel";
import { Button } from "./ui/button";
import { Alert } from "./ui/feedback";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./ui/select";
import { useActionJob } from "./useActionJob";
import { refreshSnapshot, useAppDispatch } from "../store";

export function UserGroupSettings({
  user,
  snapshot: s,
  onDirty,
}: {
  user: User;
  snapshot: Snapshot;
  onDirty: (id: string, dirty: boolean) => void;
}) {
  const dispatch = useAppDispatch();
  const seed = () => ({
    id: user.group_id || "default",
    overrides: user.group_overrides || [],
    expected: s.group_version || "",
  });
  const [base, setBase] = useState(seed),
    [form, setForm] = useState(seed);
  const dirty = JSON.stringify(form) !== JSON.stringify(base);
  useEffect(() => {
    onDirty("user.group", dirty);
    return () => onDirty("user.group", false);
  }, [dirty, onDirty]);
  useEffect(() => {
    if (!dirty) {
      const next = seed();
      setBase(next);
      setForm(next);
    }
  }, [s.group_version, dirty]);
  const job = useActionJob(() => {
    setBase(form);
    void dispatch(refreshSnapshot());
  });
  const target = groupsOf(s).find((g) => g.id === form.id);
  const locked = s.role === "slave" || job.busy || job.blocked;
  return (
    <div className="space-y-6">
      <div className="workspace-intro">
        <h2>分组与个人配置</h2>
        <p>
          选择用户所属分组，再决定哪些项跟随分组。勾选个人配置后，后续分组修改不会覆盖该项。
        </p>
      </div>
      {job.error && <Alert kind="error">{job.error}</Alert>}
      <div className="rounded-xl border p-5 space-y-5">
        <div className="grid gap-3 sm:grid-cols-[1fr_auto] sm:items-end">
          <div className="space-y-2">
            <label htmlFor="user-group">所属分组</label>
            <Select
              value={form.id}
              disabled={locked}
              onValueChange={(id) =>
                setForm({
                  ...form,
                  id,
                  overrides: groupScopes.map((k) => k.key),
                })
              }
            >
              <SelectTrigger id="user-group" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {groupsOf(s).map((g) => (
                  <SelectItem key={g.id} value={g.id}>
                    {g.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <Button variant="outline" asChild>
            <Link to={`/groups/${form.id}`}>查看分组规则</Link>
          </Button>
        </div>
        {form.id !== base.id && (
          <Alert>
            从「{groupName(s, base.id)}」移入「{target?.name}
            」时，默认保留所有当前配置。取消对应的个人配置勾选，才会采用目标分组的规则。
          </Alert>
        )}
        <div className="divide-y">
          {groupScopes.map(({ key, label, detail }) => (
            <label
              className="flex items-start justify-between gap-4 py-5"
              key={key}
            >
              <span className="min-w-0">
                <strong className="block text-sm">{label}</strong>
                <span className="block text-sm text-muted-foreground mt-1">
                  分组：{groupSummary(target?.policy || {}, key)}
                </span>
                <span className="block text-xs text-muted-foreground mt-1">
                  {detail}
                </span>
              </span>
              <span className="flex items-center gap-2 shrink-0 text-sm">
                <input
                  type="checkbox"
                  checked={form.overrides.includes(key)}
                  disabled={locked}
                  onChange={(e) =>
                    setForm({
                      ...form,
                      overrides: e.target.checked
                        ? [...form.overrides, key]
                        : form.overrides.filter((v) => v !== key),
                    })
                  }
                  aria-label={`${label}使用个人配置`}
                />
                个人配置
              </span>
            </label>
          ))}
        </div>
        <p className="text-sm text-muted-foreground">
          未勾选的项目会继承分组规则；分组未设置该项时保留当前值。直接在其他标签页修改设备名额、配额、限速、到期或线路，与分组规则不同的项会自动转为个人配置。
        </p>
        <div className="flex flex-wrap gap-2">
          <Button
            disabled={!dirty || locked}
            onClick={() =>
              void job.submit({
                action: "group.members",
                confirm: true,
                fields: {
                  request: JSON.stringify({
                    id: form.id,
                    users: [user.name],
                    mode: "custom",
                    overrides: form.overrides,
                    expected: base.expected,
                  }),
                },
              })
            }
          >
            {job.busy ? "正在保存…" : "保存分组与覆盖项"}
          </Button>
          <Button
            variant="outline"
            disabled={!dirty || locked}
            onClick={() => setForm(base)}
          >
            撤销修改
          </Button>
        </div>
      </div>
      <p className="text-sm text-muted-foreground">
        仅修改此用户。保存后需应用配置使运行中的入口策略生效；任何校验失败则全部取消，原配置保持不变。
      </p>
    </div>
  );
}
