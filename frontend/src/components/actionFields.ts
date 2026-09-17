import { inputSize } from "../format";
import type { Action, Context, Field, Snapshot } from "../types";
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
  if (action.id === "user.set" && u)
    return {
      ...context,
      quota: inputSize(u.quota),
      "quota-mode": u.quota_mode,
      "extra-quota": inputSize(u.extra_quota || 0),
      expire: u.expires,
      "up-mbps": String(u.up_mbps || 0),
      "down-mbps": String(u.down_mbps || 0),
      "billing-enabled": String(u.billing?.enabled || false),
      "billing-day": String(u.billing?.day || 1),
    };
  if (action.id === "node.set" && n)
    return {
      ...context,
      name: n.name,
      "up-mbps": String(n.up_mbps || 0),
      "down-mbps": String(n.down_mbps || 0),
    };
  if (action.id.endsWith(".access")) {
    const p = (action.id.startsWith("device.") ? d : u)?.access || {};
    return {
      ...context,
      "allow-domains": (p.allowed_domains || []).join(","),
      "block-domains": (p.blocked_domains || []).join(","),
      "block-ports": (p.blocked_ports || []).join(","),
      "max-connections": String(p.max_connections || 0),
      "connection-action": p.connection_action || "alert",
    };
  }
  if (action.id === "client.set")
    return { server: state.client.server, port: String(state.client.port) };
  return { ...context };
}
const labels: Record<string, string> = {
  true: "开启",
  false: "关闭",
  total: "双向合计",
  upload: "仅上传",
  download: "仅下载",
  enforce: "执行限制",
  monitor: "仅记录",
  dynamic: "动态单活",
  auto: "自动学习",
  manual: "固定名单",
  soft: "软封限速",
  hard: "硬封断连",
  outbound: "出站",
  endpoint: "端点",
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
    default:
      return (field.options || []).map((v) => [v, labels[v] || v]);
  }
}
export const lockedField = (field: Field, context: Context) =>
  ["user", "device", "node"].includes(field.key) && Boolean(context[field.key]);
export function collectFields(
  action: Action,
  values: Context,
  initial: Context,
  context: Context,
): Context {
  const fields: Context = {};
  for (const f of action.fields) {
    const value = values[f.key] || "";
    if (lockedField(f, context)) {
      fields[f.key] = context[f.key];
      continue;
    }
    if (f.type === "checkbox") {
      if (value === "true") fields[f.key] = "true";
      continue;
    }
    if (f.type === "multi-select") {
      fields[f.key] = value;
      continue;
    }
    if (
      value !== "" ||
      f.required ||
      (action.id === "mesh.route" && f.key === "exit")
    ) {
      if (
        f.required ||
        value !== initial[f.key] ||
        !["user.set", "node.set", "user.access", "device.access"].includes(
          action.id,
        )
      )
        fields[f.key] = value;
    }
  }
  return fields;
}
