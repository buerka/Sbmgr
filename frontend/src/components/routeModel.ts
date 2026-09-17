import type { Snapshot, User } from "../types";

export const routeTag = (id: string) => `sbmgr-mesh-${id}`;
export const routeName = (r: Snapshot["routes"][number]) => r.name || r.id;
export function routeDestination(r: Snapshot["routes"][number]) {
  if (!r.exit) return "直接出站";
  return r.name?.match(/^(.*?)\s+via\s+/i)?.[1] || r.exit;
}
export function memberName(s: Snapshot, id: string) {
  const direct = s.routes.find(
    (r) => r.entry === id && r.hops.length === 1 && r.hops[0] === id && !r.exit,
  );
  return direct?.name || id.toUpperCase();
}
export function routePath(s: Snapshot, r: Snapshot["routes"][number]) {
  return [r.entry, ...r.hops.filter((h) => h !== r.entry)]
    .map((m) => memberName(s, m))
    .concat(routeDestination(r))
    .join(" → ");
}
export function assignmentOptions(s: Snapshot, user: User, device: string) {
  const existing = user.nodes.filter((n) => n.device === device);
  const available = new Set(s.outbounds.map((o) => o.outbound));
  const items = s.routes.map((r) => ({
    outbound: routeTag(r.id),
    name: routeName(r),
    entry: memberName(s, r.entry),
    path: routePath(s, r),
    available: !s.mesh_pending && available.has(routeTag(r.id)),
  }));
  for (const o of s.outbounds) {
    if (items.some((r) => r.outbound === o.outbound)) continue;
    if (s.routes.length && s.role === "master") continue;
    items.push({
      ...o,
      entry: "本机线路",
      path: o.outbound || "默认直出",
      available: true,
    });
  }
  for (const n of existing) {
    if (!items.some((r) => r.outbound === n.outbound))
      items.push({
        outbound: n.outbound,
        name: n.name,
        entry: "已有授权",
        path: n.outbound || "默认直出",
        available: false,
      });
  }
  return items;
}
