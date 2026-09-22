import type { Snapshot, User } from "../types";

export const routeTag = (id: string) => `sbmgr-mesh-${id}`;

// Route IDs stay stable for state, subscriptions and assignments.  These
// labels are the names users see in their clients, so the topology editor
// must not expose internal host or outbound names such as "to-att via DMIT".
const clientRouteNames: Record<string, string> = {
  "dmit-direct": "LAX",
  "dmit-att": "ATT via LAX",
  "dmit-frontier": "Frontier via LAX",
  "nm-direct": "FRM",
  "nm-att": "ATT via FRM",
  "nm-frontier": "Frontier via FRM",
  "nm-gh1": "GHA 1 via FRM",
  "nm-gh2": "GHA 2 via FRM",
  "nm-gh3": "GHA 3 via FRM",
};

const clientEntryNames: Record<string, string> = {
  dmit: "LAX",
  nm: "FRM",
};

const clientDestinationNames: Record<string, string> = {
  "to-att": "ATT",
  "to-frontier": "Frontier",
  "gh-1": "GHA 1",
  "gh-2": "GHA 2",
  "gh-3": "GHA 3",
};

const legacyRouteNames: [RegExp, string][] = [
  [/^to-att\s+via\s+dmit$/i, "ATT via LAX"],
  [/^to-frontier\s+via\s+dmit$/i, "Frontier via LAX"],
  [/^to-att\s+via\s+nm$/i, "ATT via FRM"],
  [/^to-frontier\s+via\s+nm$/i, "Frontier via FRM"],
  [/^gh-1\s+via\s+nm$/i, "GHA 1 via FRM"],
  [/^gh-2\s+via\s+nm$/i, "GHA 2 via FRM"],
  [/^gh-3\s+via\s+nm$/i, "GHA 3 via FRM"],
];

function canonicalRouteName(name: string) {
  const value = name.trim();
  for (const [pattern, replacement] of legacyRouteNames) {
    if (pattern.test(value)) return replacement;
  }
  return value;
}

function clientDestination(exit: string) {
  const value = exit.trim();
  return clientDestinationNames[value.toLowerCase()] || value;
}

export function clientExitName(exit: string, fallback = "直接出站") {
  const value = exit.trim();
  return value ? clientDestination(value) : fallback;
}

export const routeName = (r: Snapshot["routes"][number]) => {
  const known = clientRouteNames[r.id.toLowerCase()];
  if (known) return known;
  if (r.name) return canonicalRouteName(r.name);
  if (!r.exit) return clientEntryNames[r.entry.toLowerCase()] || r.id;
  const entry =
    clientEntryNames[r.entry.toLowerCase()] || r.entry.toUpperCase();
  return `${clientDestination(r.exit)} via ${entry}`;
};

export function routeDestination(r: Snapshot["routes"][number]) {
  if (!r.exit) return "直接出站";
  const name = r.name?.match(/^(.*?)\s+via\s+/i)?.[1];
  return clientExitName(name || r.exit);
}
export function memberName(s: Snapshot, id: string) {
  const known = clientEntryNames[id.toLowerCase()];
  if (known) return known;
  const direct = s.routes.find(
    (r) => r.entry === id && r.hops.length === 1 && r.hops[0] === id && !r.exit,
  );
  return canonicalRouteName(direct?.name || id.toUpperCase());
}
export function routePath(s: Snapshot, r: Snapshot["routes"][number]) {
  const entries = [r.entry, ...r.hops.filter((h) => h !== r.entry)].map((m) =>
    memberName(s, m),
  );
  if (!r.exit && entries.length === 1) return entries[0];
  return entries.concat(r.exit ? routeDestination(r) : "直接出站").join(" → ");
}
export function nodeDisplayName(s: Snapshot, node: User["nodes"][number]) {
  const route = s.routes.find((r) => routeTag(r.id) === node.outbound);
  return route ? routeName(route) : canonicalRouteName(node.name);
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
        name: nodeDisplayName(s, n),
        entry: "已有授权",
        path: n.outbound || "默认直出",
        available: false,
      });
  }
  return items;
}
