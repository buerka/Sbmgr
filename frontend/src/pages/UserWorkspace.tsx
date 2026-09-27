import { useCallback, useLayoutEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import * as Tabs from "@radix-ui/react-tabs";
import {
  ActionButton,
  ActionMenu,
  Badge,
  DataTable,
  Empty,
  Panel,
} from "../components/common";
import { ActionForm } from "../components/ActionEditor";
import { UserGroupSettings } from "../components/UserGroupSettings";
import { groupName } from "../components/groupModel";
import { PortalAccess } from "../components/PortalAccess";
import { DeviceDelivery } from "../components/DeviceDelivery";

import { Progress } from "../components/ui/feedback";
import { TableCell, TableRow } from "../components/ui/table";
import { Icon, type IconName } from "../components/Icons";
import { optionLabels } from "../components/formModel";
import { bytes, dateTime, rate, userNodeSpeed } from "../format";
import { setUserDraftDirty, useAppDispatch, useAppSelector } from "../store";
import type { Snapshot, User } from "../types";
import { memberName, nodeDisplayName } from "../components/routeModel";
import "./user-workspace.css";

const speed = (n?: number) =>
  n === undefined ? "各节点不同" : n ? `${n} Mbps` : "不限";
const sections: {
  id: string;
  title: string;
  icon: IconName;
  forms: string[];
}[] = [
  { id: "basic", title: "基本设置", icon: "settings", forms: ["user.set"] },
  { id: "group", title: "分组与继承", icon: "users", forms: ["user.group"] },
  {
    id: "devices",
    title: "设备与线路",
    icon: "routes",
    forms: ["user.devices"],
  },
  {
    id: "access",
    title: "访问控制",
    icon: "shield",
    forms: ["user.ip", "user.access"],
  },
  {
    id: "protection",
    title: "流量保护",
    icon: "traffic",
    forms: ["user.burst", "user.throttle"],
  },
  { id: "delivery", title: "订阅交付", icon: "link", forms: [] },
  { id: "activity", title: "用量与记录", icon: "health", forms: [] },
  { id: "login", title: "面板登录", icon: "shield", forms: ["portal.account"] },
];
const noop = () => {};

function UserSetting({
  id,
  user,
  snapshot,
  onDirty,
}: {
  id: string;
  user: User;
  snapshot: Snapshot;
  onDirty: (id: string, dirty: boolean) => void;
}) {
  const action = useAppSelector((s) =>
    s.admin.catalog.find((a) => a.id === id),
  );
  const reportDirty = useCallback(
    (dirty: boolean) => onDirty(id, dirty),
    [id, onDirty],
  );
  if (!action)
    return (
      <Empty
        title="设置暂不可用"
        description="请刷新页面以重新加载管理操作。"
      />
    );
  return (
    <ActionForm
      action={action}
      context={{ user: user.name }}
      snapshot={snapshot}
      onClose={noop}
      embedded
      onDirtyChange={reportDirty}
    />
  );
}

export function UserDetail() {
  const { name } = useParams();
  const s = useAppSelector((s) => s.admin.snapshot)!;
  const u = s.users.find((u) => u.name === name);
  return u ? (
    <UserWorkspace key={u.name} u={u} s={s} />
  ) : (
    <Empty
      title="用户不存在"
      description="请从用户列表重新选择。"
      icon="users"
    />
  );
}

function UserWorkspace({ u, s }: { u: User; s: Snapshot }) {
  const dispatch = useAppDispatch();
  const [tab, setTab] = useState("basic");
  const [drafts, setDrafts] = useState<Record<string, boolean>>({});
  const dirty = Object.values(drafts).some(Boolean);
  const reportDirty = useCallback((id: string, value: boolean) => {
    setDrafts((current) =>
      current[id] === value ? current : { ...current, [id]: value },
    );
  }, []);
  useLayoutEffect(() => {
    dispatch(setUserDraftDirty(dirty));
    return () => {
      dispatch(setUserDraftDirty(false));
    };
  }, [dirty, dispatch]);
  const context = { user: u.name },
    quota = u.quota + u.extra_quota;
  return (
    <div className="user-workspace-page">
      <Link to="/users" className="back-link">
        <Icon name="back" />
        用户管理
      </Link>
      <div className="profile-header">
        <div className="flex items-center gap-4 min-w-0">
          <div className="profile-avatar shrink-0">
            {u.name.slice(0, 2).toUpperCase()}
          </div>
          <div className="min-w-0">
            <div className="flex items-center gap-3 flex-wrap">
              <h1 className="break-all">{u.name}</h1>
              <Badge kind={u.status === "已启用" ? "success" : "warning"}>
                {u.status}
              </Badge>
            </div>
            <p className="text-sm text-muted-foreground mt-1">
              {groupName(s, u.group_id)} · {u.devices.length} 台设备 ·{" "}
              {u.nodes.length} 个授权节点 ·{" "}
              {u.expires ? `有效至 ${u.expires}` : "长期有效"}
            </p>
          </div>
        </div>
        <p className="workspace-save-status" role="status">
          <span className={dirty ? "draft-dot" : "saved-dot"} />
          {dirty ? "有未保存的修改" : "当前设置已载入"}
        </p>
      </div>
      <div className="user-summary">
        <div>
          <p>本期用量</p>
          <strong>
            {bytes(u.used)} <small>/ {u.quota ? bytes(quota) : "不限"}</small>
          </strong>
          <Progress
            label="用户配额使用率"
            value={u.quota ? (u.used / quota) * 100 : 0}
          />
        </div>
        <div>
          <p>实时下载</p>
          <strong>{rate(u.current_down)}</strong>
          <small>上传 {rate(u.current_up)}</small>
        </div>
        <div>
          <p>速度上限</p>
          <strong>{speed(userNodeSpeed(u, "down"))}</strong>
          <small>上传 {speed(userNodeSpeed(u, "up"))}</small>
        </div>
        <div>
          <p>计费方式</p>
          <strong>{optionLabels[u.quota_mode] || "双向合计"}</strong>
          <small>
            {u.billing?.enabled
              ? `每月 ${u.billing.cycle_day} 日重置`
              : "手动管理账期"}
          </small>
        </div>
      </div>
      <Tabs.Root value={tab} onValueChange={setTab}>
        <Tabs.List
          className="tab-list workspace-tabs"
          aria-label="用户配置分类"
        >
          {sections.map((section) => (
            <Tabs.Trigger key={section.id} value={section.id}>
              <Icon name={section.icon} />
              {section.title}
              {section.forms.some((id) => drafts[id]) && (
                <span className="draft-dot" aria-label="有未保存修改" />
              )}
            </Tabs.Trigger>
          ))}
        </Tabs.List>
        <Tabs.Content
          value="group"
          className="tab-content workspace-tab"
          forceMount
        >
          <UserGroupSettings user={u} snapshot={s} onDirty={reportDirty} />
        </Tabs.Content>
        <Tabs.Content
          value="login"
          className="tab-content workspace-tab"
          forceMount
        >
          <PortalAccess
            user={u}
            disabled={s.role === "slave"}
            onDirty={reportDirty}
          />
        </Tabs.Content>
        <Tabs.Content
          value="basic"
          className="tab-content workspace-tab"
          forceMount
        >
          <div className="workspace-intro">
            <h2>基本设置</h2>
            <p>
              管理此用户的配额、账期、有效期和速率。修改后的值若与分组规则不同，会自动成为个人覆盖；可在「分组与继承」中切回继承。
            </p>
          </div>
          <UserSetting
            id="user.set"
            user={u}
            snapshot={s}
            onDirty={reportDirty}
          />
          <section className="workspace-account-actions">
            <div>
              <h2>账号管理</h2>
              <p>
                启停与解除封禁保存后需应用配置。删除用户会撤销其设备与节点授权。
              </p>
            </div>
            <div className="flex flex-wrap gap-2">
              <ActionButton
                id={u.enabled ? "user.disable" : "user.enable"}
                context={context}
              />
              <ActionButton id="user.unblock" context={context} />
              <ActionButton id="user.clone" context={{ from: u.name }} />
              <ActionButton
                id="user.delete"
                disabled={dirty}
                title={dirty ? "请先保存或重置未保存的修改" : undefined}
                context={context}
                variant="ghost"
                className="text-destructive"
              />
            </div>
          </section>
        </Tabs.Content>
        <Tabs.Content
          value="devices"
          className="tab-content workspace-tab"
          forceMount
        >
          <div className="section-toolbar">
            <div>
              <h2>已授权设备</h2>
              <p className="text-sm text-muted-foreground mt-1">
                每台设备拥有独立订阅和节点授权。
              </p>
            </div>
            <ActionButton id="device.add" context={context} />
          </div>
          <div className="mb-6">
            <UserSetting
              id="user.devices"
              user={u}
              snapshot={s}
              onDirty={reportDirty}
            />
            <p className="text-sm text-muted-foreground mt-3">
              已使用 {u.devices.length} 台 /{" "}
              {u.device_limit ? `${u.device_limit} 台名额` : "自助管理未开放"}
              。需要先在「面板登录」邀请用户开通账号。
            </p>
          </div>
          {!u.devices.length && (
            <Empty
              title="还没有设备"
              description="新增设备后，为它分配可用线路。"
              icon="device"
            />
          )}
          {u.devices.map((d) => {
            const dc = { ...context, device: d.name },
              nodes = u.nodes.filter((n) => n.device === d.name);
            return (
              <section className="device-workspace" key={d.name}>
                <div className="device-heading">
                  <div className="flex items-center gap-3">
                    <span className="surface-icon">
                      <Icon name="device" size={20} />
                    </span>
                    <div>
                      <h3>{d.label || d.name}</h3>
                      <p className="text-xs text-muted-foreground mt-1">
                        {nodes.length} 个节点 · {bytes(d.upload + d.download)}{" "}
                        累计用量
                      </p>
                    </div>
                    <Badge kind={d.enabled ? "success" : "default"}>
                      {d.enabled ? "已启用" : "已停用"}
                    </Badge>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <ActionButton id="node.assign" context={dc}>
                      分配线路
                    </ActionButton>
                    <ActionButton id="device.ip" context={dc}>
                      来源规则
                    </ActionButton>
                    <ActionButton id="device.access" context={dc}>
                      访问规则
                    </ActionButton>
                    <ActionMenu
                      label="设备设置"
                      items={[
                        "node.add",
                        d.enabled ? "device.disable" : "device.enable",
                        "device.rotate-link",
                        "device.rotate",
                        "device.delete",
                      ].map((id) => ({ id, context: dc }))}
                    />
                  </div>
                </div>
                {nodes.length ? (
                  <DataTable
                    headings={[
                      "节点",
                      "入口",
                      "累计用量",
                      "上传 / 下载上限",
                      "操作",
                    ]}
                  >
                    {nodes.map((n) => {
                      const displayName = nodeDisplayName(s, n);
                      return (
                        <TableRow key={n.name}>
                          <TableCell>
                            <span className="inline-flex items-center gap-2 font-medium">
                              <Icon
                                name="routes"
                                className="text-muted-foreground"
                              />
                              {displayName}
                            </span>
                          </TableCell>
                          <TableCell>
                            <Badge kind="default">
                              {memberName(s, n.entry)}
                            </Badge>
                          </TableCell>
                          <TableCell>{bytes(n.upload + n.download)}</TableCell>
                          <TableCell>
                            {speed(n.up_mbps)} / {speed(n.down_mbps)}
                          </TableCell>
                          <TableCell>
                            <div className="flex gap-1">
                              <ActionButton
                                id="node.set"
                                context={{ ...dc, node: n.name }}
                                variant="ghost"
                                size="sm"
                              >
                                编辑
                              </ActionButton>
                              <ActionMenu
                                label={`更多：${displayName}`}
                                compact
                                items={[
                                  {
                                    id: "node.delete",
                                    context: { ...dc, node: n.name },
                                  },
                                ]}
                              />
                            </div>
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </DataTable>
                ) : (
                  <Empty
                    title="尚未分配节点"
                    description="从已有线路中选择，此设备的订阅会自动更新。"
                    icon="routes"
                  />
                )}
              </section>
            );
          })}
        </Tabs.Content>
        <Tabs.Content
          value="access"
          className="tab-content workspace-tab"
          forceMount
        >
          <div className="workspace-intro">
            <h2>访问控制</h2>
            <p>
              设置整个用户的来源
              IP、域名、端口和并发规则。设备自己的规则在「设备与线路」中设置。
            </p>
          </div>
          <div className="workspace-form-stack">
            <UserSetting
              id="user.ip"
              user={u}
              snapshot={s}
              onDirty={reportDirty}
            />
            <UserSetting
              id="user.access"
              user={u}
              snapshot={s}
              onDirty={reportDirty}
            />
          </div>
        </Tabs.Content>
        <Tabs.Content
          value="protection"
          className="tab-content workspace-tab"
          forceMount
        >
          <div className="workspace-intro">
            <h2>流量保护</h2>
            <p>
              为此用户设置异常流量处理和阶梯限速；关闭时仍保留所填参数。保存后需应用配置。
            </p>
          </div>
          <div className="workspace-form-stack">
            <UserSetting
              id="user.burst"
              user={u}
              snapshot={s}
              onDirty={reportDirty}
            />
            <UserSetting
              id="user.throttle"
              user={u}
              snapshot={s}
              onDirty={reportDirty}
            />
          </div>
        </Tabs.Content>
        <Tabs.Content
          value="delivery"
          className="tab-content workspace-tab"
          forceMount
        >
          <div className="workspace-intro">
            <h2>订阅交付</h2>
            <p>
              仅显示 {u.name} 的设备。可复制订阅链接、下载 TXT 或
              YAML，也可轮换单台设备的订阅链接。
            </p>
          </div>
          <DeviceDelivery user={u} />
        </Tabs.Content>
        <Tabs.Content
          value="activity"
          className="tab-content workspace-tab"
          forceMount
        >
          <div className="section-toolbar">
            <div>
              <h2>用量与记录</h2>
              <p className="text-sm text-muted-foreground mt-1">
                本期上传 {bytes(u.upload)} · 下载 {bytes(u.download)}
                {u.billing?.next_reset
                  ? ` · 下次重置 ${dateTime(u.billing.next_reset)}`
                  : ""}
              </p>
            </div>
            <ActionButton id="user.reset" context={context}>
              重置本期用量
            </ActionButton>
          </div>
          <Panel title="活动连接" description="最近 50 项连接记录。">
            {u.connections.length ? (
              <DataTable headings={["设备 / 节点", "来源", "目标", "开始时间"]}>
                {u.connections.slice(0, 50).map((c, i) => (
                  <TableRow key={i}>
                    <TableCell>
                      {c.device} / {c.node}
                    </TableCell>
                    <TableCell>{c.source}</TableCell>
                    <TableCell>{c.target}</TableCell>
                    <TableCell>{dateTime(c.since)}</TableCell>
                  </TableRow>
                ))}
              </DataTable>
            ) : (
              <Empty
                title="当前没有活动连接"
                description="客户端建立连接后会显示在这里。"
                icon="link"
              />
            )}
          </Panel>
          <Panel title="最近访问" description="最近 100 项访问记录。">
            {u.accesses?.length ? (
              <DataTable headings={["目标", "设备 / 节点", "次数", "最后访问"]}>
                {u.accesses.map((a, i) => (
                  <TableRow key={i}>
                    <TableCell>{a.target}</TableCell>
                    <TableCell>
                      {a.device} / {a.node}
                    </TableCell>
                    <TableCell>{a.count}</TableCell>
                    <TableCell>{dateTime(a.last_seen)}</TableCell>
                  </TableRow>
                ))}
              </DataTable>
            ) : (
              <Empty title="暂无访问记录" />
            )}
          </Panel>
        </Tabs.Content>
      </Tabs.Root>
    </div>
  );
}
