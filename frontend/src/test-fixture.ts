import type { Action, Snapshot, User } from "./types";
export function fixtureUser(name = "alice"): User {
  return {
    name,
    enabled: true,
    status: "已启用",
    quota: 100 * 2 ** 30,
    extra_quota: 0,
    quota_mode: "total",
    used: 0,
    upload: 0,
    download: 0,
    expires: "",
    up_mbps: 0,
    down_mbps: 0,
    current_up: 0,
    current_down: 0,
    devices: [
      {
        name: "phone",
        enabled: true,
        upload: 0,
        download: 0,
        deliverable: true,
      },
    ],
    nodes: [],
    connections: [],
    accesses: [],
  };
}
export function fixtureSnapshot(): Snapshot {
  return {
    version: "test",
    time: "2026-01-01T00:00:00Z",
    role: "standalone",
    revision: 0,
    pending: false,
    mesh_pending: false,
    users: [fixtureUser()],
    outbounds: [],
    proxies: [],
    members: [],
    routes: [],
    backups: [],
    health: {},
    fleet: [],
    alerts: [],
    audit: [],
    client: { server: "relay.example", port: 443 },
    subscription: { enabled: false, base_url: "", listen: "", template: "" },
  };
}
export const quotaAction: Action = {
  id: "user.set",
  title: "配额与限速",
  scope: "user",
  effect: "保存后需要应用配置。",
  danger: false,
  fields: [
    {
      key: "user",
      label: "用户",
      type: "select",
      required: true,
      source: "users",
    },
    { key: "quota", label: "配额", type: "text", required: false },
    { key: "expire", label: "到期日", type: "date", required: false },
    {
      key: "clear-expire",
      label: "清除到期日",
      type: "checkbox",
      required: false,
    },
  ],
};
