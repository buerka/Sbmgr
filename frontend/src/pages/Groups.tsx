import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import * as Tabs from "@radix-ui/react-tabs";
import { PageHeader, Badge, Empty } from "../components/common";
import { Icon } from "../components/Icons";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Alert } from "../components/ui/feedback";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "../components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../components/ui/select";
import { useActionJob } from "../components/useActionJob";
import {
  groupName,
  groupsOf,
  groupScopes,
  groupSummary,
} from "../components/groupModel";
import { assignmentOptions } from "../components/routeModel";
import { inputSize } from "../format";
import {
  refreshSnapshot,
  setUserDraftDirty,
  useAppDispatch,
  useAppSelector,
} from "../store";
import type {
  GroupPolicy,
  GroupScope,
  Snapshot,
  User,
  UserGroup,
} from "../types";

export function Groups() {
  const s = useAppSelector((s) => s.admin.snapshot)!;
  const [search, setSearch] = useState("");
  return (
    <>
      <PageHeader
        title="用户分组"
        description="统一维护规则，为每位用户保留独立配额与个性化设置。"
        actions={
          <>
            <Button variant="outline" asChild>
              <Link to="/users">用户列表</Link>
            </Button>
            {s.role !== "slave" && (
              <Button asChild>
                <Link to="/groups/new">
                  <Icon name="add" />
                  新建分组
                </Link>
              </Button>
            )}
          </>
        }
      />
      <Input
        className="max-w-sm mb-6"
        aria-label="搜索分组"
        placeholder="搜索分组…"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
      />
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {groupsOf(s)
          .filter((g) =>
            g.name.toLowerCase().includes(search.trim().toLowerCase()),
          )
          .map((g) => {
            const users = s.users.filter(
              (u) => (u.group_id || "default") === g.id,
            );
            return (
              <Link
                className="group rounded-xl border bg-card p-6 transition-colors hover:bg-accent/40 focus-visible:outline-2 focus-visible:outline-ring"
                key={g.id}
                to={`/groups/${g.id}`}
              >
                <div className="flex items-center justify-between gap-2 mb-5">
                  <h2 className="font-semibold break-all">{g.name}</h2>
                  {g.id === "default" ? (
                    <Badge>默认</Badge>
                  ) : (
                    <Icon name="next" />
                  )}
                </div>
                <p className="text-3xl font-semibold tabular-nums">
                  {users.length}
                  <span className="text-sm font-normal text-muted-foreground ml-2">
                    位成员
                  </span>
                </p>
                <p className="text-xs text-muted-foreground mt-2 mb-5">
                  {users.filter((u) => u.group_overrides?.length).length}{" "}
                  位成员有个人配置
                </p>
                <div className="space-y-3 border-t pt-4">
                  {groupScopes.map(({ key, label }) => (
                    <div key={key}>
                      <p className="text-xs text-muted-foreground">{label}</p>
                      <p className="text-sm mt-1">
                        {groupSummary(g.policy, key)}
                      </p>
                    </div>
                  ))}
                </div>
              </Link>
            );
          })}
      </div>
      <p className="text-sm text-muted-foreground mt-6">
        默认分组初始不强制任何规则，原有用户保持现有配置。分组规则变更只影响未设置个人覆盖的成员。
      </p>
    </>
  );
}

function policyForm(g: UserGroup) {
  const p = g.policy;
  return {
    name: g.name,
    enabled: groupScopes
      .filter((k) => p[k.key] !== undefined)
      .map((k) => k.key),
    quota: inputSize(p.quota?.bytes || 0),
    mode: p.quota?.mode || "total",
    up: String(p.rate?.upload || 0),
    down: String(p.rate?.download || 0),
    expiry: p.expiry || "",
    devices: String(p.devices || 0),
    routes: p.routes || [],
  };
}
function parseQuota(value: string) {
  const m = value
    .trim()
    .match(/^(\d+(?:\.\d+)?)\s*(B|K|M|G|T|KiB|MiB|GiB|TiB|KB|MB|GB|TB)?$/i);
  if (!m) throw new Error("配额请输入 100G 这样的数值，0 表示不限。");
  const unit = (m[2] || "G").toUpperCase()[0],
    n = Number(m[1]) * 1024 ** Math.max(0, "BKMGT".indexOf(unit));
  if (!Number.isSafeInteger(n))
    throw new Error("配额超出范围或包含不足一个字节的小数。");
  return n;
}
function toPolicy(f: ReturnType<typeof policyForm>): GroupPolicy {
  const p: GroupPolicy = {};
  if (f.enabled.includes("devices")) {
    const n = Number(f.devices);
    if (!f.devices.trim() || !Number.isInteger(n) || n < 0 || n > 100)
      throw new Error("设备名额须为 0–100 的整数。");
    p.devices = n;
  }
  if (f.enabled.includes("quota"))
    p.quota = { bytes: parseQuota(f.quota), mode: f.mode };
  if (f.enabled.includes("rate")) {
    const upload = Number(f.up),
      download = Number(f.down);
    if (
      !f.up.trim() ||
      !f.down.trim() ||
      !Number.isFinite(upload) ||
      !Number.isFinite(download) ||
      upload < 0 ||
      download < 0
    )
      throw new Error("速度上限必须是非负数，0 表示不限。");
    p.rate = { upload, download };
  }
  if (f.enabled.includes("expiry")) p.expiry = f.expiry;
  if (f.enabled.includes("routes")) {
    if (!f.routes.length) throw new Error("请至少选择一条分组线路。");
    p.routes = f.routes;
  }
  return p;
}

export function GroupDetail() {
  const s = useAppSelector((s) => s.admin.snapshot)!,
    { id } = useParams();
  const g =
    id === "new"
      ? { id: "", name: "", policy: {} }
      : groupsOf(s).find((g) => g.id === id);
  return g ? (
    <GroupWorkspace key={id} g={g} s={s} />
  ) : (
    <Empty title="分组不存在" description="请返回分组列表重新选择。" />
  );
}
function GroupWorkspace({ g, s }: { g: UserGroup; s: Snapshot }) {
  const navigate = useNavigate(),
    dispatch = useAppDispatch();
  const [base, setBase] = useState(() => policyForm(g)),
    [form, setForm] = useState(() => policyForm(g)),
    [expected, setExpected] = useState(s.group_version || ""),
    [tab, setTab] = useState("rules"),
    [deleteOpen, setDeleteOpen] = useState(false);
  const dirty = JSON.stringify(base) !== JSON.stringify(form);
  const members = s.users.filter((u) => (u.group_id || "default") === g.id);
  useEffect(() => {
    if (!dirty) {
      const next = policyForm(g);
      setBase(next);
      setForm(next);
      setExpected(s.group_version || "");
    }
  }, [s.group_version, dirty]);
  useEffect(() => {
    dispatch(setUserDraftDirty(dirty));
    return () => {
      dispatch(setUserDraftDirty(false));
    };
  }, [dirty, dispatch]);
  const job = useActionJob(() => {
    dispatch(setUserDraftDirty(false));
    setBase(form);
    setDeleteOpen(false);
    void dispatch(refreshSnapshot());
    if (!g.id || deleteOpen) navigate("/groups");
  });
  const locked = s.role === "slave" || job.busy || job.blocked;
  const toggle = (key: GroupScope) =>
    setForm({
      ...form,
      enabled: form.enabled.includes(key)
        ? form.enabled.filter((k) => k !== key)
        : [...form.enabled, key],
    });
  const options = assignmentOptions(s, { nodes: [] } as unknown as User, "");
  for (const route of form.routes) {
    if (!options.some((o) => o.outbound === route.outbound))
      options.push({
        ...route,
        entry: "原线路",
        path: route.name,
        available: false,
      });
  }
  function submit(e: FormEvent) {
    e.preventDefault();
    try {
      const policy = toPolicy(form);
      void job.submit({
        action: "group.save",
        confirm: true,
        fields: {
          request: JSON.stringify({
            id: g.id,
            name: form.name,
            policy,
            expected,
          }),
        },
      });
    } catch (e) {
      job.setError(e instanceof Error ? e.message : "请检查表单");
    }
  }
  return (
    <>
      <Link className="back-link" to="/groups">
        <Icon name="back" />
        用户分组
      </Link>
      <PageHeader
        title={g.id ? g.name : "新建分组"}
        description={
          g.id
            ? `${members.length} 位成员 · 统一规则与个人覆盖分别管理。`
            : "先设置分组规则，再将用户移入此分组。"
        }
        actions={
          g.id && g.id !== "default" ? (
            <Button
              variant="outline"
              disabled={locked || members.length > 0 || dirty}
              title={members.length ? "请先移出全部成员" : undefined}
              onClick={() => setDeleteOpen(true)}
            >
              删除空分组
            </Button>
          ) : undefined
        }
      />
      {job.error && (
        <Alert kind="error" className="mb-5">
          {job.error}
        </Alert>
      )}
      <Tabs.Root value={tab} onValueChange={setTab}>
        <Tabs.List className="tab-list" aria-label="分组配置分类">
          <Tabs.Trigger value="rules">
            统一规则{dirty && <span className="draft-dot" />}
          </Tabs.Trigger>
          <Tabs.Trigger value="members" disabled={!g.id}>
            成员管理{" "}
            <span className="ml-2 text-muted-foreground">{members.length}</span>
          </Tabs.Trigger>
        </Tabs.List>
        <Tabs.Content
          value="rules"
          className="tab-content"
          forceMount
          hidden={tab !== "rules"}
        >
          <form onSubmit={submit} className="space-y-5 max-w-4xl">
            <div className="rounded-xl border p-5 space-y-2">
              <label htmlFor="group-name">分组名称</label>
              <Input
                id="group-name"
                required
                maxLength={80}
                value={form.name}
                disabled={locked}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            </div>
            {groupScopes.map(({ key, label, detail }) => {
              const enabled = form.enabled.includes(key),
                covered = members.filter(
                  (u) => !u.group_overrides?.includes(key),
                ).length;
              return (
                <section
                  key={key}
                  className="rounded-xl border p-5 sm:p-6 space-y-5"
                >
                  <label className="flex items-start gap-3 cursor-pointer">
                    <input
                      type="checkbox"
                      className="mt-1"
                      checked={enabled}
                      disabled={locked}
                      onChange={() => toggle(key)}
                      aria-label={`统一${label}`}
                    />
                    <span className="flex-1">
                      <strong className="block text-base">统一{label}</strong>
                      <span className="block text-sm text-muted-foreground mt-1">
                        {detail}
                      </span>
                    </span>
                    <span className="text-xs text-muted-foreground shrink-0 mt-1">
                      {covered} 人继承
                    </span>
                  </label>
                  {!enabled ? (
                    <p className="text-sm text-muted-foreground pl-7">
                      关闭时保留所有成员当前的值，不再随分组更新。
                    </p>
                  ) : (
                    <div className="space-y-4 sm:pl-7">
                      {key === "devices" && (
                        <div className="space-y-2">
                          <label htmlFor="group-devices">
                            每位用户的设备总名额
                          </label>
                          <Input
                            id="group-devices"
                            className="max-w-xs"
                            type="number"
                            min="0"
                            max="100"
                            step="1"
                            disabled={locked}
                            value={form.devices}
                            onChange={(e) =>
                              setForm({ ...form, devices: e.target.value })
                            }
                          />
                          <p className="text-sm text-muted-foreground">
                            0 关闭自助管理；1–100
                            为设备总名额，包含已有和停用设备。减少名额不删除已有设备，超额期间不能新增。保存后名额立即生效。
                          </p>
                          <p className="text-xs text-muted-foreground">
                            设备共用用户配额；动态单活、来源 IP
                            和并发规则继续有效。用户只能复制自己已有设备的线路与限制。
                          </p>
                        </div>
                      )}
                      {key === "quota" && (
                        <div className="grid gap-4 sm:grid-cols-2">
                          <div className="space-y-2">
                            <label htmlFor="group-quota">每位用户的配额</label>
                            <Input
                              id="group-quota"
                              value={form.quota}
                              disabled={locked}
                              onChange={(e) =>
                                setForm({ ...form, quota: e.target.value })
                              }
                              placeholder="如 100G；0 不限"
                            />
                            <p className="text-xs text-muted-foreground">
                              每人独立使用，不共享组内流量。裸数字按 GiB。
                            </p>
                          </div>
                          <div className="space-y-2">
                            <label htmlFor="group-mode">计费方向</label>
                            <Select
                              value={form.mode}
                              disabled={locked}
                              onValueChange={(mode) =>
                                setForm({ ...form, mode })
                              }
                            >
                              <SelectTrigger id="group-mode" className="w-full">
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                <SelectItem value="total">双向合计</SelectItem>
                                <SelectItem value="upload">仅上传</SelectItem>
                                <SelectItem value="download">仅下载</SelectItem>
                              </SelectContent>
                            </Select>
                          </div>
                        </div>
                      )}
                      {key === "rate" && (
                        <div className="grid gap-4 sm:grid-cols-2">
                          {(
                            [
                              ["up", "上传"],
                              ["down", "下载"],
                            ] as const
                          ).map(([k, label]) => (
                            <div key={k} className="space-y-2">
                              <label htmlFor={`group-${k}`}>
                                {label}上限（Mbps）
                              </label>
                              <Input
                                id={`group-${k}`}
                                type="number"
                                min="0"
                                step="any"
                                value={form[k]}
                                disabled={locked}
                                onChange={(e) =>
                                  setForm({ ...form, [k]: e.target.value })
                                }
                              />
                              <p className="text-xs text-muted-foreground">
                                0 表示不限速，作用于每个节点。
                              </p>
                            </div>
                          ))}
                        </div>
                      )}
                      {key === "expiry" && (
                        <div className="space-y-2">
                          <label htmlFor="group-expiry">有效期最后一天</label>
                          <Input
                            id="group-expiry"
                            className="max-w-xs"
                            type="date"
                            disabled={locked}
                            value={form.expiry}
                            onChange={(e) =>
                              setForm({ ...form, expiry: e.target.value })
                            }
                          />
                          <p className="text-xs text-muted-foreground">
                            留空表示长期有效。此设置不会启用已手动停用的账号。
                          </p>
                        </div>
                      )}
                      {key === "routes" && (
                        <>
                          <div className="grid gap-2 sm:grid-cols-2 max-h-80 overflow-y-auto">
                            {options.map((o) => {
                              const checked = form.routes.some(
                                (r) => r.outbound === o.outbound,
                              );
                              return (
                                <label
                                  key={o.outbound}
                                  className={`flex items-start gap-3 rounded-lg border p-3 ${checked ? "bg-accent/50" : ""}`}
                                >
                                  <input
                                    type="checkbox"
                                    className="mt-1"
                                    disabled={
                                      locked || (!o.available && !checked)
                                    }
                                    checked={checked}
                                    onChange={(e) =>
                                      setForm({
                                        ...form,
                                        routes: e.target.checked
                                          ? [
                                              ...form.routes,
                                              {
                                                name: o.name,
                                                outbound: o.outbound,
                                              },
                                            ]
                                          : form.routes.filter(
                                              (r) => r.outbound !== o.outbound,
                                            ),
                                      })
                                    }
                                  />
                                  <span className="min-w-0">
                                    <span className="block text-sm font-medium break-words">
                                      {o.name}
                                    </span>
                                    <span className="text-xs text-muted-foreground">
                                      {o.available
                                        ? o.path
                                        : "不可用，请先应用线路拓扑"}
                                    </span>
                                  </span>
                                </label>
                              );
                            })}
                          </div>
                          <p className="text-xs text-muted-foreground">
                            已选 {form.routes.length}{" "}
                            条。会替换继承成员每台设备的授权集合；仍被保留的节点身份、名称及用量不变。
                          </p>
                        </>
                      )}
                      <p className="text-xs text-muted-foreground">
                        保存会更新 {covered} 位继承成员，保留{" "}
                        {members.length - covered} 位成员的个人配置。
                      </p>
                    </div>
                  )}
                </section>
              );
            })}
            <div className="rounded-xl border bg-card p-5 space-y-4">
              <p className="text-sm text-muted-foreground">
                保存时所有成员在同一事务中更新，任何校验失败则全部取消。不会重置累计用量、订阅链接或登录密码。保存后需应用配置使运行中的入口策略生效。
              </p>
              <div className="flex gap-2">
                <Button type="submit" disabled={locked || (!dirty && !!g.id)}>
                  {job.busy ? "正在保存…" : g.id ? "保存分组规则" : "创建分组"}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  disabled={locked || !dirty}
                  onClick={() => setForm(base)}
                >
                  撤销修改
                </Button>
              </div>
            </div>
          </form>
        </Tabs.Content>
        <Tabs.Content
          value="members"
          className="tab-content"
          forceMount
          hidden={tab !== "members"}
        >
          {g.id && <GroupMembers g={g} s={s} disabled={dirty} />}
        </Tabs.Content>
      </Tabs.Root>
      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent>
          <DialogTitle>删除空分组「{g.name}」？</DialogTitle>
          <DialogDescription>
            仅删除此分组。默认分组与所有用户保持不变。
          </DialogDescription>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteOpen(false)}>
              取消
            </Button>
            <Button
              disabled={locked}
              onClick={() =>
                void job.submit({
                  action: "group.delete",
                  confirm: true,
                  fields: {
                    request: JSON.stringify({
                      id: g.id,
                      expected: s.group_version,
                    }),
                  },
                })
              }
            >
              删除空分组
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function GroupMembers({
  g,
  s,
  disabled,
}: {
  g: UserGroup;
  s: Snapshot;
  disabled: boolean;
}) {
  const dispatch = useAppDispatch(),
    [search, setSearch] = useState(""),
    [selected, setSelected] = useState<string[]>([]),
    [target, setTarget] = useState(g.id),
    [mode, setMode] = useState("preserve"),
    [expected, setExpected] = useState(s.group_version);
  const job = useActionJob(() => {
    setSelected([]);
    void dispatch(refreshSnapshot());
  });
  const locked = disabled || s.role === "slave" || job.busy || job.blocked;
  const users = s.users
    .filter((u) => u.name.toLowerCase().includes(search.trim().toLowerCase()))
    .sort(
      (a, b) =>
        Number((b.group_id || "default") === g.id) -
          Number((a.group_id || "default") === g.id) ||
        a.name.localeCompare(b.name),
    );
  function select(name: string, on: boolean) {
    if (!selected.length) setExpected(s.group_version);
    setSelected(on ? [...selected, name] : selected.filter((v) => v !== name));
  }
  return (
    <div className="space-y-5 max-w-4xl">
      <div>
        <h2>成员与移入</h2>
        <p className="text-sm text-muted-foreground mt-2">
          当前分组成员排在前面。可选择其他分组的用户移入，或将现有成员移出。
        </p>
      </div>
      {disabled && <Alert>请先保存或撤销规则草稿，再调整成员。</Alert>}
      {job.error && <Alert kind="error">{job.error}</Alert>}
      <Input
        aria-label="搜索分组成员"
        placeholder="搜索用户…"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
      />
      <div className="rounded-xl border divide-y max-h-96 overflow-y-auto">
        {users.map((u) => (
          <label
            key={u.name}
            className="flex items-center gap-3 p-4 hover:bg-accent/30"
          >
            <input
              type="checkbox"
              disabled={locked}
              checked={selected.includes(u.name)}
              onChange={(e) => select(u.name, e.target.checked)}
            />
            <span className="flex-1 min-w-0">
              <strong className="block text-sm">{u.name}</strong>
              <span className="text-xs text-muted-foreground">
                {groupName(s, u.group_id)} · {u.group_overrides?.length || 0}{" "}
                项个人配置
              </span>
            </span>
            <Link
              className="text-sm underline underline-offset-4"
              to={`/users/${encodeURIComponent(u.name)}`}
            >
              配置用户
            </Link>
          </label>
        ))}
      </div>
      <div className="rounded-xl border p-5 space-y-4">
        <p className="text-sm font-medium">已选择 {selected.length} 位用户</p>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <label htmlFor="target-group">目标分组</label>
            <Select value={target} disabled={locked} onValueChange={setTarget}>
              <SelectTrigger id="target-group" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {groupsOf(s).map((g) => (
                  <SelectItem value={g.id} key={g.id}>
                    {g.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <label htmlFor="member-mode">配置处理方式</label>
            <Select value={mode} disabled={locked} onValueChange={setMode}>
              <SelectTrigger id="member-mode" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="preserve">
                  保留当前配置，设为个人覆盖
                </SelectItem>
                <SelectItem value="inherit">
                  清除个人覆盖，继承分组规则
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>
        <p className="text-sm text-muted-foreground">
          {mode === "preserve"
            ? "保留所选用户当前的四项配置；后续可在用户页逐项切回继承。"
            : "所选用户的四项个人覆盖将被清除，并采用目标分组已设置的规则。分组未设置的项保留当前值。"}{" "}
          不重置用量或登录密码，任一成员失败则全部取消；保存后需应用配置。
        </p>
        <Button
          disabled={locked || !selected.length}
          onClick={() =>
            void job.submit({
              action: "group.members",
              confirm: true,
              fields: {
                request: JSON.stringify({
                  id: target,
                  users: selected,
                  mode,
                  expected,
                }),
              },
            })
          }
        >
          {job.busy ? "正在更新…" : `更新 ${selected.length} 位成员`}
        </Button>
      </div>
    </div>
  );
}
