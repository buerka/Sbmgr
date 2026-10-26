import { describe, expect, it } from "vitest";
import { bytes, inputSize, rate, userNodeSpeed } from "./format";
import type { User } from "./types";

describe("bytes", () => {
  it("formats zero and binary units", () => {
    expect(bytes()).toBe("0 B");
    expect(bytes(0)).toBe("0 B");
    expect(bytes(1024)).toBe("1.0 KiB");
    expect(bytes(1536)).toBe("1.5 KiB");
    expect(bytes(1024 ** 3)).toBe("1.0 GiB");
  });

  it("caps the unit at TiB", () => {
    expect(bytes(1024 ** 5)).toBe("1024.0 TiB");
  });
});

describe("rate and inputSize", () => {
  it("renders fixed two-decimal rates", () => {
    expect(rate(0)).toBe("0.00 Mbps");
    expect(rate(12.345)).toBe("12.35 Mbps");
  });

  it("keeps whole GiB values terse in inputs", () => {
    expect(inputSize(0)).toBe("0");
    expect(inputSize(2 ** 30)).toBe("1G");
    expect(inputSize(1536)).toBe("1536B");
  });
});

describe("userNodeSpeed", () => {
  type UserOverrides = Omit<Partial<User>, "nodes"> & {
    nodes?: Partial<User["nodes"][number]>[];
  };
  const user = (overrides: UserOverrides = {}) =>
    ({
      name: "alice",
      up_mbps: 0,
      down_mbps: 0,
      nodes: [],
      ...overrides,
    }) as User;

  it("prefers the user-level limit", () => {
    const value = user({ up_mbps: 25, nodes: [{ up_mbps: 10 }] });
    expect(userNodeSpeed(value, "up")).toBe(25);
  });

  it("uses a shared node limit and hides mixed limits", () => {
    expect(
      userNodeSpeed(user({ nodes: [{ up_mbps: 0 }, { up_mbps: 0 }] }), "up"),
    ).toBe(0);
    expect(
      userNodeSpeed(
        user({ nodes: [{ down_mbps: 10 }, { down_mbps: 20 }] }),
        "down",
      ),
    ).toBeUndefined();
  });

  it("returns zero when the user has no nodes", () => {
    expect(userNodeSpeed(user(), "down")).toBe(0);
  });
});
