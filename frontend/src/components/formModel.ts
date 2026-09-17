import { inputSize } from "../format";
import type { Action, Context, Field, Snapshot } from "../types";
export const patchActions = new Set([
  "user.set",
  "node.set",
  "user.ip",
  "device.ip",
  "user.burst",
  "user.throttle",
  "user.access",
  "device.access",
  "subscription.set",
  "health.set",
  "client.set",
  "template.set",
]);
export const selectionKeys = new Set(["user", "device", "node"]);
export const listFields = new Set([
  "allow-domains",
  "block-domains",
  "block-ports",
  "ip-allowed",
  "ip-temp",
  "targets",
]);
const num = (value: number | undefined, fallback = 0) =>
  String(value ?? fallback);
export function initialFields(
  action: Action,
  context: Context,
  state: Snapshot,
): Context {
  const u = state.users.find((u) => u.name === context.user),
    d = u?.devices.find((d) => d.name === context.device),
    n = u?.nodes.find(
      (n) =>
        n.name === context.node &&
        (!context.device || n.device === context.device),
    );
  let fields: Context = {};
  if (action.id === "user.set" && u)
    fields = {
      quota: inputSize(u.quota),
      "quota-mode": u.quota_mode,
      "extra-quota": inputSize(u.extra_quota),
      expire: u.expires,
      "up-mbps": num(u.up_mbps),
      "down-mbps": num(u.down_mbps),
      "billing-enabled": String(Boolean(u.billing?.enabled)),
      "billing-day": num(u.billing?.cycle_day, 1),
    };
  if (action.id === "node.set" && n)
    fields = {
      name: n.name,
      "up-mbps": num(n.up_mbps),
      "down-mbps": num(n.down_mbps),
    };
  if (action.id === "user.ip" || action.id === "device.ip") {
    const p = (action.id === "device.ip" ? d : u)?.ip_policy;
    if (p)
      fields = {
        "ip-enabled": String(Boolean(p.enabled)),
        "ip-mode": p.mode || "enforce",
        "ip-binding": p.binding || "dynamic",
        "ip-max": num(p.max_ips, 1),
        "ip-handover-seconds": num(p.handover_seconds, 60),
        "ip-allowed": (p.bound_ips || []).join(","),
        "ip-temp": (p.temporary_ips || []).join(","),
        "ip-temp-minutes": p.temporary_until
          ? String(
              Math.max(
                0,
                Math.ceil(
                  (Date.parse(p.temporary_until) - Date.parse(state.time)) /
                    60000,
                ),
              ),
            )
          : "",
      };
  }
  if (action.id === "user.burst" && u) {
    const p = u.burst || {};
    fields = {
      "burst-enabled": String(Boolean(p.enabled)),
      "burst-window": num(p.window_minutes),
      "burst-limit": inputSize(p.limit_bytes || 0),
      "burst-block": num(p.block_minutes),
      "burst-action": p.action || "hard",
      "burst-soft-up-kbps": num(p.soft_upload_kbps),
      "burst-soft-down-kbps": num(p.soft_download_kbps),
    };
  }
  if (action.id === "user.throttle" && u) {
    const p = u.throttle || {};
    fields = {
      tiered: String(Boolean(p.enabled)),
      "tier1-usage": num(p.tier1_usage_percent),
      "tier1-speed": num(p.tier1_speed_percent),
      "tier2-usage": num(p.tier2_usage_percent),
      "tier2-speed": num(p.tier2_speed_percent),
    };
  }
  if (action.id.endsWith(".access")) {
    const p = (action.id.startsWith("device.") ? d : u)?.access || {};
    fields = {
      "allow-domains": (p.allowed_domains || []).join(","),
      "block-domains": (p.blocked_domains || []).join(","),
      "block-ports": (p.blocked_ports || []).join(","),
      "max-connections": num(p.max_connections),
      "connection-action": p.connection_action || "alert",
    };
  }
  if (action.id === "subscription.set")
    fields = {
      enabled: String(state.subscription.enabled),
      listen: state.subscription.listen,
      "base-url": state.subscription.base_url,
    };
  if (action.id === "template.set")
    fields = { path: state.subscription.template_path || "" };
  if (action.id === "health.set" && state.health_settings) {
    const p = state.health_settings;
    fields = {
      mode: p.mode,
      interval: num(p.interval_minutes),
      timeout: num(p.timeout_seconds),
      failures: num(p.alert_after_failures),
      targets: Object.entries(p.targets || {})
        .map(([tag, target]) => `${tag}=${target}`)
        .join(","),
    };
  }
  if (action.id === "client.set")
    fields = { server: state.client.server, port: String(state.client.port) };
  if (action.id === "mesh.route") {
    const r = state.routes.find((r) => r.id === context.id);
    if (r)
      fields = {
        id: r.id,
        entry: r.entry,
        hops: r.hops.join(","),
        exit: r.exit,
        protocols: r.protocols?.length
          ? new Set(r.protocols).size === 1
            ? r.protocols[0]
            : r.protocols.join(",")
          : "",
      };
  }
  if (action.id === "mesh.entry") {
    const m = state.members.find((m) => m.id === context.id);
    fields = { port: "443" };
    if (m?.client)
      fields = {
        server: m.client.server,
        port: String(m.client.port),
        server_name: m.client.server_name || "",
        reality_public_key: m.client.reality_public_key || "",
        short_id: m.client.short_id || "",
      };
  }
  if (action.id === "user.add")
    fields = {
      quota: "0",
      "quota-mode": "total",
      "node-name": "默认节点",
      "up-mbps": "0",
      "down-mbps": "0",
    };
  return { ...context, ...fields };
}
export const optionLabels: Record<string, string> = {
  true: "开启",
  false: "关闭",
  total: "双向合计",
  upload: "仅上传",
  download: "仅下载",
  enforce: "执行限制",
  monitor: "仅观察",
  dynamic: "动态单活",
  auto: "自动学习",
  manual: "固定名单",
  soft: "限速保护",
  hard: "临时断开",
  alert: "仅告警",
  "disable-device": "停用设备",
  "disable-user": "停用用户",
  outbound: "出站",
  endpoint: "端点",
  off: "关闭",
};
export function fieldOptions(
  field: Field,
  values: Context,
  state: Snapshot,
): [string, string][] {
  const u = state.users.find((u) => u.name === values.user);
  switch (field.source) {
    case "users":
      return state.users.map((u) => [u.name, u.name]);
    case "devices":
      return (u?.devices || []).map((d) => [d.name, d.name]);
    case "nodes":
      return (u?.nodes || [])
        .filter((n) => !values.device || n.device === values.device)
        .map((n) => [n.name, n.name]);
    case "members":
      return state.members.map((m) => [m.id, m.id]);
    case "outbounds":
      return state.outbounds.map((o) => [o.outbound, o.name]);
    case "backups":
      return state.backups.map((b) => [b.name, b.name]);
    default: {
      const options: [string, string][] = (field.options || []).map((v) => [
        v,
        field.key === "mode" && v === "auto"
          ? "定时检查"
          : optionLabels[v] || v,
      ]);
      if (field.key === "protocols" && values.protocols?.includes(","))
        options.push([
          values.protocols,
          `逐跳：${values.protocols.split(",").join(" → ")}`,
        ]);
      return options;
    }
  }
}
export const lockedField = (field: Field, context: Context) =>
  (selectionKeys.has(field.key) || ["id", "tag", "kind"].includes(field.key)) &&
  Boolean(context[field.key]);
export function isEdit(action: Action, context: Context) {
  return (
    patchActions.has(action.id) ||
    action.id === "mesh.entry" ||
    (action.id === "mesh.route" && Boolean(context.id))
  );
}
export function changedFields(
  action: Action,
  values: Context,
  initial: Context,
  context: Context,
) {
  return action.fields.filter(
    (f) =>
      !lockedField(f, context) &&
      !(selectionKeys.has(f.key) && f.source) &&
      (values[f.key] || "") !== (initial[f.key] || ""),
  );
}
export function collectFields(
  action: Action,
  values: Context,
  initial: Context,
  context: Context,
): Context {
  const fields: Context = {},
    patch = patchActions.has(action.id);
  for (const f of action.fields) {
    const value = values[f.key] ?? "";
    if (lockedField(f, context)) {
      fields[f.key] = context[f.key];
      continue;
    }
    if (
      f.key === "expire" &&
      action.id === "user.set" &&
      (values["clear-expire"] === "true" || (!value && initial.expire))
    ) {
      fields["clear-expire"] = "true";
      continue;
    }
    if (f.type === "checkbox") {
      if (value === "true") fields[f.key] = "true";
      continue;
    }
    if (
      (patch ||
        (action.id === "mesh.route" && f.key === "protocols" && context.id)) &&
      !f.required &&
      value === (initial[f.key] ?? "")
    )
      continue;
    if (patch && listFields.has(f.key) && value !== initial[f.key]) {
      fields[f.key] = value || (f.key === "targets" ? "" : "-");
      continue;
    }
    if (
      value ||
      f.required ||
      (action.id === "mesh.route" && f.key === "exit") ||
      (patch && initial[f.key])
    )
      fields[f.key] = value;
  }
  if (action.id === "user.ip" && fields["ip-temp"] && fields["ip-temp"] !== "-")
    fields["ip-temp-minutes"] = values["ip-temp-minutes"] || "";
  return fields;
}
