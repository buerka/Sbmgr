import type { GroupPolicy, GroupScope, Snapshot } from "../types";
import { bytes } from "../format";
export const groupScopes: { key: GroupScope; label: string; detail: string }[] =
  [
    {
      key: "quota",
      label: "流量配额",
      detail: "每位用户独立计算；附加流量和账期保持个人设置。",
    },
    {
      key: "rate",
      label: "速度上限",
      detail: "统一设置每个节点的速率，不合并组内带宽。",
    },
    {
      key: "expiry",
      label: "到期时间",
      detail: "统一有效期，不影响账号的手动启停。",
    },
    {
      key: "routes",
      label: "线路授权",
      detail: "作用于用户的每台设备；保留线路的身份与用量不变。",
    },
  ];
export const groupsOf = (s: Snapshot) =>
  s.groups || [{ id: "default", name: "默认分组", policy: {} }];
export const groupName = (s: Snapshot, id?: string) =>
  groupsOf(s).find((g) => g.id === (id || "default"))?.name || "默认分组";
export function groupSummary(p: GroupPolicy, key: GroupScope) {
  if (p[key] === undefined) return "未统一设置，保留个人当前配置";
  switch (key) {
    case "quota":
      return `${p.quota!.bytes ? bytes(p.quota!.bytes) : "不限流量"} / 人 · ${{ total: "双向合计", upload: "仅上传", download: "仅下载" }[p.quota!.mode] || p.quota!.mode}`;
    case "rate":
      return `上传 ${p.rate!.upload || "不限"}${p.rate!.upload ? " Mbps" : ""} · 下载 ${p.rate!.download || "不限"}${p.rate!.download ? " Mbps" : ""}`;
    case "expiry":
      return p.expiry || "长期有效";
    case "routes":
      return `${p.routes!.length} 条线路 · 每台设备`;
  }
}
