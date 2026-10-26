import { describe, expect, it } from "vitest";
import type { Snapshot } from "../types";
import {
  canonicalOutbound,
  clientExitName,
  routeName,
  routeTag,
} from "./routeModel";

const snapshot = (routes: Snapshot["routes"]) => ({ routes }) as Snapshot;

describe("routeTag", () => {
  it("uses the stable kernel prefix", () => {
    expect(routeTag("chain")).toBe("sbmgr-mesh-chain");
  });
});

describe("canonicalOutbound", () => {
  const state = snapshot([
    {
      id: "chain",
      entry: "master",
      hops: ["relay", "exit"],
      exit: "direct",
      outbound_aliases: ["to-exit"],
    },
  ]);

  it("keeps mesh tags and resolves legacy aliases", () => {
    expect(canonicalOutbound(state, routeTag("chain"))).toBe(routeTag("chain"));
    expect(canonicalOutbound(state, "to-exit")).toBe(routeTag("chain"));
  });

  it("leaves unrelated outbounds unchanged", () => {
    expect(canonicalOutbound(state, "direct")).toBe("direct");
  });
});

describe("routeName", () => {
  it("prefers explicit labels and known ids", () => {
    expect(routeName({ id: "chain", label: "主线路" } as never)).toBe("主线路");
    expect(routeName({ id: "dmit-direct" } as never)).toBe("LAX");
  });

  it("derives a client name from an exit and entry", () => {
    expect(
      routeName({ id: "custom", entry: "dmit", exit: "to-att" } as never),
    ).toBe("ATT via LAX");
    expect(
      routeName({ id: "custom", entry: "nm", exit: "to-frontier" } as never),
    ).toBe("Frontier via FRM");
  });

  it("canonicalizes legacy route descriptions", () => {
    expect(
      routeName({ id: "legacy", name: "gh-2 via nm", entry: "nm" } as never),
    ).toBe("GHA 2 via FRM");
  });
});

describe("clientExitName", () => {
  it("maps known destinations and keeps the fallback", () => {
    expect(clientExitName("to-frontier")).toBe("Frontier");
    expect(clientExitName("custom-exit")).toBe("custom-exit");
    expect(clientExitName("")).toBe("直接出站");
  });
});
