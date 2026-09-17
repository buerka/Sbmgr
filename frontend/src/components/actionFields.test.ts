import { describe, expect, it } from "vitest";
import { collectFields, fieldOptions, initialFields } from "./actionFields";
import { fixtureSnapshot, quotaAction } from "../test-fixture";
import type { Action } from "../types";
describe("management field boundaries", () => {
  it("preserves the complete client entry when updating one field", () => {
    const state = fixtureSnapshot();
    state.members = [
      {
        id: "edge",
        host: "relay.example",
        master: false,
        client: {
          server: "relay.example",
          port: 443,
          server_name: "www.example.com",
          reality_public_key: "fixture-public-key",
          short_id: "abcd",
        },
      },
    ];
    const action: Action = {
      ...quotaAction,
      id: "mesh.entry",
      fields: [
        "id",
        "server",
        "port",
        "server_name",
        "reality_public_key",
        "short_id",
      ].map((key) => ({
        key,
        label: key,
        type: "text",
        required: key !== "short_id",
      })),
    };
    const context = { id: "edge" },
      initial = initialFields(action, context, state);
    // This endpoint replaces the entry object; optional unchanged fields must be sent.
    expect(
      collectFields(action, { ...initial, port: "8443" }, initial, context),
    ).toEqual({ ...initial, port: "8443" });
    expect(initialFields(action, { id: "new-edge" }, state).port).toBe("443");
  });
  it("sends only changed quota and locked identity; clearing an existing expiry removes it", () => {
    const state = fixtureSnapshot();
    state.users[0].expires = "2027-01-01";
    const context = { user: "alice" },
      initial = initialFields(quotaAction, context, state);
    expect(
      collectFields(
        quotaAction,
        { ...initial, user: "bob", quota: "120G", expire: "" },
        initial,
        context,
      ),
    ).toEqual({ user: "alice", quota: "120G", "clear-expire": "true" });
  });
  it("requires explicit expiry reset and keeps an explicit zero quota", () => {
    const context = { user: "alice" },
      initial = initialFields(quotaAction, context, fixtureSnapshot());
    expect(
      collectFields(
        quotaAction,
        { ...initial, quota: "0", "clear-expire": "true" },
        initial,
        context,
      ),
    ).toEqual({ user: "alice", quota: "0", "clear-expire": "true" });
  });
  it("keeps blank exit for a direct route, and limits node choices to the chosen device", () => {
    const action: Action = {
      id: "mesh.route",
      title: "线路",
      scope: "routes",
      danger: false,
      effect: "",
      fields: [{ key: "exit", label: "出口", required: false, type: "text" }],
    };
    expect(collectFields(action, { exit: "" }, {}, {})).toEqual({ exit: "" });
    const state = fixtureSnapshot();
    state.users[0].nodes = [
      { name: "one", device: "phone" },
      { name: "two", device: "laptop" },
    ] as (typeof state.users)[0]["nodes"];
    expect(
      fieldOptions(
        {
          key: "node",
          label: "节点",
          type: "select",
          required: true,
          source: "nodes",
        },
        { user: "alice", device: "phone" },
        state,
      ),
    ).toEqual([["one", "one"]]);
  });
  it("loads saved IP, billing, burst and throttle values rather than form defaults", () => {
    const s = fixtureSnapshot(),
      u = s.users[0],
      ctx = { user: "alice" };
    u.billing = { enabled: true, cycle_day: 19 };
    u.ip_policy = {
      enabled: true,
      mode: "monitor",
      binding: "manual",
      max_ips: 3,
      handover_seconds: 90,
      bound_ips: ["192.0.2.10"],
      temporary_ips: ["198.51.100.2"],
      temporary_until: "2026-01-01T00:30:00Z",
    };
    u.burst = {
      enabled: true,
      window_minutes: 15,
      limit_bytes: 2 ** 31,
      block_minutes: 10,
      action: "soft",
      soft_upload_kbps: 128,
      soft_download_kbps: 512,
    };
    u.throttle = {
      enabled: true,
      tier1_usage_percent: 75,
      tier1_speed_percent: 60,
      tier2_usage_percent: 90,
      tier2_speed_percent: 20,
    };
    expect(initialFields(quotaAction, ctx, s)["billing-day"]).toBe("19");
    expect(
      initialFields({ ...quotaAction, id: "user.ip" }, ctx, s),
    ).toMatchObject({
      "ip-enabled": "true",
      "ip-mode": "monitor",
      "ip-binding": "manual",
      "ip-max": "3",
      "ip-handover-seconds": "90",
      "ip-allowed": "192.0.2.10",
      "ip-temp": "198.51.100.2",
      "ip-temp-minutes": "30",
    });
    expect(
      initialFields({ ...quotaAction, id: "user.burst" }, ctx, s),
    ).toMatchObject({
      "burst-window": "15",
      "burst-limit": "2G",
      "burst-block": "10",
      "burst-action": "soft",
      "burst-soft-down-kbps": "512",
    });
    expect(
      initialFields({ ...quotaAction, id: "user.throttle" }, ctx, s),
    ).toMatchObject({
      tiered: "true",
      "tier1-usage": "75",
      "tier2-speed": "20",
    });
  });
  it("clears saved lists explicitly and does not renew temporary access on unrelated edits", () => {
    const action: Action = {
      ...quotaAction,
      id: "user.ip",
      fields: [
        quotaAction.fields[0],
        ...["ip-allowed", "ip-temp", "ip-temp-minutes", "ip-mode"].map(
          (key) => ({
            key,
            label: key,
            type: "text" as const,
            required: false,
          }),
        ),
      ],
    };
    const initial = {
      user: "alice",
      "ip-allowed": "192.0.2.10",
      "ip-temp": "198.51.100.2",
      "ip-temp-minutes": "30",
      "ip-mode": "monitor",
    };
    expect(
      collectFields(
        action,
        { ...initial, "ip-allowed": "", "ip-mode": "enforce" },
        initial,
        { user: "alice" },
      ),
    ).toEqual({ user: "alice", "ip-allowed": "-", "ip-mode": "enforce" });
    expect(
      collectFields(
        action,
        { ...initial, "ip-temp": "198.51.100.3" },
        initial,
        { user: "alice" },
      ),
    ).toEqual({
      user: "alice",
      "ip-temp": "198.51.100.3",
      "ip-temp-minutes": "30",
    });
  });
  it("shows mixed route protocols and preserves them when changing only an exit", () => {
    const s = fixtureSnapshot();
    s.routes = [
      {
        id: "route-a",
        entry: "master",
        hops: ["edge-a", "edge-b"],
        exit: "old-exit",
        protocols: ["socks", "wireguard"],
      },
    ];
    const action: Action = {
      ...quotaAction,
      id: "mesh.route",
      fields: [
        ...["id", "entry", "hops", "exit", "protocols"].map((key) => ({
          key,
          label: key,
          type: "text" as const,
          required: ["id", "entry", "hops"].includes(key),
        })),
      ],
    };
    const initial = initialFields(action, { id: "route-a" }, s);
    expect(initial.protocols).toBe("socks,wireguard");
    expect(
      collectFields(action, { ...initial, exit: "new-exit" }, initial, {
        id: "route-a",
      }),
    ).toEqual({
      id: "route-a",
      entry: "master",
      hops: "edge-a,edge-b",
      exit: "new-exit",
    });
  });
});
