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
import { Button } from "../components/ui/button";
import { Progress } from "../components/ui/feedback";
import { TableCell, TableRow } from "../components/ui/table";
import { Icon } from "../components/Icons";
import { optionLabels } from "../components/formModel";
import { bytes, dateTime, rate } from "../format";
import { useAppSelector } from "../store";
import type { Context } from "../types";
import { memberName, nodeDisplayName } from "../components/routeModel";
const speed = (n?: number) => (n ? `${n} Mbps` : "不限");
function SettingCard({
  title,
  description,
  action,
  context,
  rows,
}: {
  title: string;
  description: string;
  action: string;
  context: Context;
  rows: [string, string][];
}) {
  return (
    <section className="setting-card">
      <div className="flex justify-between items-center gap-3">
        <h2>{title}</h2>
        <ActionButton
          id={action}
          context={context}
          variant="ghost"
          size="sm"
          aria-label={`编辑${title}`}
        >
          编辑
        </ActionButton>
      </div>
      <p className="text-sm text-muted-foreground mt-1">{description}</p>
      <dl className="settings-list">
        {rows.map(([k, v]) => (
          <div key={k}>
            <dt>{k}</dt>
            <dd>{v}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
export function UserDetail() {
  const { name } = useParams(),
    s = useAppSelector((s) => s.admin.snapshot)!,
    u = s.users.find((u) => u.name === name);
  if (!u)
    return (
      <Empty
        title="用户不存在"
        description="请从用户列表重新选择。"
        icon="users"
      />
    );
  const context = { user: u.name },
    quota = u.quota + u.extra_quota;
  return (
    <>
      <Link to="/users" className="back-link">
        <Icon name="back" />
        用户管理
      </Link>
      <div className="profile-header">
        <div className="flex items-center gap-4">
          <div className="profile-avatar">
            {u.name.slice(0, 2).toUpperCase()}
          </div>
          <div>
            <div className="flex items-center gap-3">
              <h1>{u.name}</h1>
              <Badge kind={u.status === "已启用" ? "success" : "warning"}>
                {u.status}
              </Badge>
            </div>
            <p className="text-sm text-muted-foreground mt-1">
              {u.devices.length} 台设备 · {u.nodes.length} 个授权节点 ·{" "}
              {u.expires ? `有效至 ${u.expires}` : "长期有效"}
            </p>
          </div>
        </div>
        <div className="flex gap-2">
          <ActionButton id="node.assign" context={context} variant="default">
            分配线路
          </ActionButton>
          <ActionButton id="user.set" context={context}>
            编辑配额
          </ActionButton>
          <ActionMenu
            label="用户操作"
            items={[
              { id: u.enabled ? "user.disable" : "user.enable", context },
              { id: "user.unblock", context },
              { id: "user.reset", context, divider: true },
              { id: "user.delete", context },
            ]}
          />
        </div>
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
          <strong>{speed(u.down_mbps)}</strong>
          <small>上传 {speed(u.up_mbps)}</small>
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
      <Tabs.Root defaultValue="devices">
        <Tabs.List className="tab-list" aria-label="用户详情分类">
          <Tabs.Trigger value="devices">设备与节点</Tabs.Trigger>
          <Tabs.Trigger value="policies">配额与策略</Tabs.Trigger>
          <Tabs.Trigger value="activity">连接记录</Tabs.Trigger>
        </Tabs.List>
        <Tabs.Content value="devices" className="tab-content">
          <div className="section-toolbar">
            <div>
              <h2>已授权设备</h2>
              <p className="text-sm text-muted-foreground mt-1">
                每台设备拥有独立订阅和节点授权。
              </p>
            </div>
            <ActionButton id="device.add" context={context} />
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
                      <h3>{d.name}</h3>
                      <p className="text-xs text-muted-foreground mt-1">
                        {nodes.length} 个节点 · {bytes(d.upload + d.download)}{" "}
                        累计用量
                      </p>
                    </div>
                    <Badge kind={d.enabled ? "success" : "default"}>
                      {d.enabled ? "已启用" : "已停用"}
                    </Badge>
                  </div>
                  <div className="flex gap-2">
                    <ActionButton id="node.assign" context={dc}>
                      分配线路
                    </ActionButton>
                    <ActionMenu
                      label="设备设置"
                      items={[
                        "node.add",
                        "device.ip",
                        "device.access",
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
          <Button asChild variant="outline">
            <Link to="/subscriptions">
              <Icon name="link" />
              管理设备订阅
            </Link>
          </Button>
        </Tabs.Content>
        <Tabs.Content value="policies" className="tab-content">
          <p className="text-sm text-muted-foreground mb-5">
            以下为当前保存的设置。编辑时会带入原值，保存后按提示应用配置。
          </p>
          <div className="settings-grid">
            <SettingCard
              title="配额与限速"
              description="决定用户可以使用多少流量，以及最高速率。"
              action="user.set"
              context={context}
              rows={[
                ["流量配额", u.quota ? bytes(u.quota) : "不限"],
                ["附加流量", bytes(u.extra_quota)],
                ["上传 / 下载", `${speed(u.up_mbps)} / ${speed(u.down_mbps)}`],
                ["有效期", u.expires || "长期有效"],
                [
                  "自动账期",
                  u.billing?.enabled
                    ? `每月 ${u.billing.cycle_day} 日`
                    : "关闭",
                ],
              ]}
            />
            <SettingCard
              title="来源 IP"
              description="控制哪些网络来源可以使用此用户。"
              action="user.ip"
              context={context}
              rows={[
                ["当前状态", u.ip_policy?.enabled ? "开启" : "关闭"],
                ["绑定方式", optionLabels[u.ip_policy?.binding || "dynamic"]],
                ["最多来源", `${u.ip_policy?.max_ips ?? 1} 个 IP`],
                ["固定名单", u.ip_policy?.bound_ips?.join(", ") || "无"],
                ["换绑宽限", `${u.ip_policy?.handover_seconds ?? 60} 秒`],
              ]}
            />
            <SettingCard
              title="访问与并发"
              description="按域名、端口和连接数管理访问。"
              action="user.access"
              context={context}
              rows={[
                ["允许域名", u.access?.allowed_domains?.join(", ") || "不限"],
                ["拒绝域名", u.access?.blocked_domains?.join(", ") || "无"],
                ["拒绝端口", u.access?.blocked_ports?.join(", ") || "无"],
                [
                  "连接上限",
                  u.access?.max_connections
                    ? String(u.access.max_connections)
                    : "不限",
                ],
                [
                  "超限处理",
                  optionLabels[u.access?.connection_action || "alert"],
                ],
              ]}
            />
            <SettingCard
              title="异常流量保护"
              description="短时间内用量异常时，自动保护线路。"
              action="user.burst"
              context={context}
              rows={[
                ["当前状态", u.burst?.enabled ? "开启" : "关闭"],
                ["检测窗口", `${u.burst?.window_minutes || 0} 分钟`],
                ["流量阈值", bytes(u.burst?.limit_bytes)],
                ["处理方式", optionLabels[u.burst?.action || "hard"]],
                ["保护时长", `${u.burst?.block_minutes || 0} 分钟`],
              ]}
            />
            <SettingCard
              title="阶梯限速"
              description="接近流量配额时，逐档降低可用速率。"
              action="user.throttle"
              context={context}
              rows={[
                ["当前状态", u.throttle?.enabled ? "开启" : "关闭"],
                [
                  "第一档",
                  `${u.throttle?.tier1_usage_percent || 0}% 用量 → ${u.throttle?.tier1_speed_percent || 0}% 速率`,
                ],
                [
                  "第二档",
                  `${u.throttle?.tier2_usage_percent || 0}% 用量 → ${u.throttle?.tier2_speed_percent || 0}% 速率`,
                ],
              ]}
            />
          </div>
        </Tabs.Content>
        <Tabs.Content value="activity" className="tab-content">
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
    </>
  );
}
