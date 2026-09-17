export type Context = Record<string, string>;
export interface Session {
  csrf: string;
  username?: string;
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
  enabled: boolean;
  upload: number;
  download: number;
  deliverable: boolean;
  assignment_version?: string;
  access?: AccessPolicy;
  ip_policy?: IPPolicy;
}
export interface IPPolicy {
  enabled?: boolean;
  mode?: string;
  binding?: string;
  max_ips?: number;
  handover_seconds?: number;
  bound_ips?: string[];
  temporary_ips?: string[];
  temporary_until?: string;
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
export interface User {
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
  ip_policy?: IPPolicy;
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
export interface ClientEntry {
  server: string;
  port: number;
  server_name?: string;
  reality_public_key?: string;
  short_id?: string;
}
export interface Snapshot {
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
    entry: string;
    hops: string[];
    exit: string;
    protocols?: string[];
  }[];
  backups: { name: string; size: number; modified: string }[];
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
