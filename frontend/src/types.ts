export type Context = Record<string, string>;
export interface Session {
  csrf: string;
  username?: string;
  role?: "admin" | "user";
}
export interface AccessPolicy {
  allowed_domains?: string[];
  blocked_domains?: string[];
  blocked_ports?: number[];
  max_connections?: number;
  connection_action?: string;
}
export interface Device {
  name: string;
  label?: string;
  enabled: boolean;
  upload: number;
  download: number;
  deliverable: boolean;
  assignment_version?: string;
  access?: AccessPolicy;
}
export interface BurstPolicy {
  enabled?: boolean;
  window_minutes?: number;
  limit_bytes?: number;
  block_minutes?: number;
  action?: string;
  soft_upload_kbps?: number;
  soft_download_kbps?: number;
}
export interface ThrottlePolicy {
  enabled?: boolean;
  tier1_usage_percent?: number;
  tier1_speed_percent?: number;
  tier2_usage_percent?: number;
  tier2_speed_percent?: number;
}
export interface Node {
  name: string;
  device: string;
  outbound: string;
  entry: string;
  upload: number;
  download: number;
  up_mbps: number;
  down_mbps: number;
  current_up: number;
  current_down: number;
}
export type GroupScope = "quota" | "rate" | "expiry" | "routes" | "devices";
export interface GroupPolicy {
  devices?: number;
  quota?: { bytes: number; mode: string };
  rate?: { upload: number; download: number };
  expiry?: string;
  routes?: { outbound: string; name: string }[];
}
export interface UserGroup {
  id: string;
  name: string;
  policy: GroupPolicy;
}
export interface User {
  device_limit?: number;
  group_id?: string;
  group_overrides?: GroupScope[];
  portal?: {
    configured: boolean;
    enabled: boolean;
    invited: boolean;
    invite_expires: string;
  };
  name: string;
  enabled: boolean;
  status: string;
  quota: number;
  extra_quota: number;
  quota_mode: string;
  used: number;
  upload: number;
  download: number;
  expires: string;
  up_mbps: number;
  down_mbps: number;
  current_up: number;
  current_down: number;
  devices: Device[];
  nodes: Node[];
  access?: AccessPolicy;
  billing?: {
    enabled?: boolean;
    cycle_day?: number;
    time_zone?: string;
    next_reset?: string;
  };
  burst?: BurstPolicy;
  throttle?: ThrottlePolicy;
  connections: {
    device: string;
    node: string;
    source: string;
    target: string;
    since: string;
  }[];
  accesses: {
    target: string;
    device: string;
    node: string;
    first_seen: string;
    last_seen: string;
    count: number;
  }[];
}
export interface PortalSnapshot {
  device_version: string;
  pending: boolean;
  time: string;
  subscription_enabled: boolean;
  user: Pick<
    User,
    | "device_limit"
    | "name"
    | "enabled"
    | "status"
    | "quota"
    | "extra_quota"
    | "quota_mode"
    | "used"
    | "upload"
    | "download"
    | "expires"
    | "up_mbps"
    | "down_mbps"
    | "current_up"
    | "current_down"
    | "billing"
  > & {
    devices: Device[];
    nodes: (Pick<
      Node,
      "name" | "device" | "upload" | "download" | "current_up" | "current_down"
    > & { available: boolean })[];
  };
}
export interface AnalyticsSnapshot {
  user: string;
  device: string;
  days: 1 | 7 | 30;
  from: string;
  to: string;
  coverage: {
    status: "collecting" | "partial" | "unavailable";
    first_seen: string;
    last_seen: string;
    gaps: number;
    note: string;
  };
  totals: {
    upload: number;
    download: number;
    connections: number;
    domains: number;
  };
  devices: {
    name: string;
    label: string;
    upload: number;
    download: number;
    connections: number;
    last_seen: string;
  }[];
  series: {
    date: string;
    upload: number;
    download: number;
    connections: number;
  }[];
  domains: {
    domain: string;
    upload: number;
    download: number;
    connections: number;
    last_seen: string;
  }[];
  pagination: { page: number; page_size: number; total: number };
  recent: {
    domain: string;
    device: string;
    label: string;
    started_at: string;
    closed_at: string;
    upload: number;
    download: number;
    status: string;
  }[];
}
export interface SiteBlocksSnapshot {
  domains: string[];
  version: string;
  pending: boolean;
  limit: number;
  message?: string;
}
export interface ClientEntry {
  server: string;
  port: number;
  server_name?: string;
  reality_public_key?: string;
  short_id?: string;
}
export interface MachineTrafficRecord {
  member: string;
  anchor_start?: string;
  interval?: number;
  unit?: "day" | "month" | "year";
  future?: boolean;
  next_reset?: string;
  effective_start_at?: string;
  effective_end_at?: string;
  period_start: string;
  period_end: string;
  upload_bytes: number;
  download_bytes: number;
  total_bytes: number;
  covered_seconds: number;
  period_seconds: number;
  coverage_percent: number;
  first_sample_at: string;
  last_sample_at: string;
  status:
    | "unconfigured"
    | "collecting"
    | "complete"
    | "no_data"
    | "error"
    | "settled"
    | "future";
  note: string;
}
export interface MachineTrafficHistory {
  member: string;
  page: number;
  page_size: number;
  total: number;
  periods: MachineTrafficRecord[];
}
export interface Snapshot {
  machine_traffic?: MachineTrafficRecord[];
  groups?: UserGroup[];
  group_version?: string;
  version: string;
  time: string;
  role: "standalone" | "master" | "slave";
  revision: number;
  pending: boolean;
  mesh_pending: boolean;
  users: User[];
  outbounds: { name: string; outbound: string }[];
  proxies: { kind: string; tag: string; type: string }[];
  members: {
    id: string;
    host: string;
    master: boolean;
    client?: ClientEntry;
  }[];
  routes: {
    id: string;
    name?: string;
    outbound_aliases?: string[];
    entry: string;
    hops: string[];
    exit: string;
    protocols?: string[];
  }[];
  backups: {
    name: string;
    size: number;
    modified: string;
    expires?: string;
    protected?: string;
  }[];
  backup_settings?: { retention_days: number };
  backup_storage?: {
    total_bytes: number;
    state_bytes: number;
    other_bytes: number;
  };
  health: Record<string, { tag: string; target: string; healthy: boolean }>;
  health_settings?: {
    mode: string;
    interval_minutes: number;
    timeout_seconds: number;
    alert_after_failures: number;
    targets?: Record<string, string>;
  };
  fleet: { name: string; host: string; online: boolean; checked: string }[];
  alerts: { user: string; kind: string; message: string }[];
  audit: { at: string; actor: string; action: string }[];
  client: ClientEntry;
  subscription: {
    enabled: boolean;
    base_url: string;
    listen: string;
    template: boolean;
    template_path?: string;
    tls_configured?: boolean;
  };
}
export interface RouteInventory {
  member: string;
  exits: { tag: string; name: string; type: string }[];
}
export interface Field {
  key: string;
  label: string;
  type:
    | "text"
    | "number"
    | "date"
    | "checkbox"
    | "select"
    | "multi-select"
    | "secret-text";
  required: boolean;
  options?: string[];
  source?: string;
}
export interface Action {
  id: string;
  title: string;
  scope: string;
  effect: string;
  danger: boolean;
  fields: Field[];
}
export interface ActionInput {
  action: string;
  fields: Context;
  confirm: boolean;
}
export interface Job {
  id: string;
  title: string;
  status: "running" | "success" | "failed";
  message: string;
}
export interface ActionTarget {
  id: string;
  context: Context;
}
