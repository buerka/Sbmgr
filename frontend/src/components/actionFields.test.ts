import { describe, expect, it } from "vitest";
import { collectFields, fieldOptions, initialFields } from "./actionFields";
import { fixtureSnapshot, quotaAction } from "../test-fixture";
import type { Action } from "../types";
describe("management field boundaries", () => {
  it("sends only changed quota and locked identity; blank expiry preserves the existing value", () => {
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
    ).toEqual({ user: "alice", quota: "120G" });
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
});
