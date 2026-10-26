import { describe, expect, it } from "vitest";
import { deliveryFilename } from "./downloadFilename";

describe("deliveryFilename", () => {
  it("falls back to a safe extension when the header is absent", () => {
    expect(deliveryFilename(null, "yaml")).toBe("subscription.yaml");
    expect(deliveryFilename("", "link")).toBe("subscription.txt");
  });

  it("accepts plain and quoted filenames", () => {
    expect(
      deliveryFilename('attachment; filename="alice-phone.yaml"', "yaml"),
    ).toBe("alice-phone.yaml");
    // Quotes are rejected after unescaping because the value is written to a
    // local path; the safe fallback wins over a partially valid name.
    expect(
      deliveryFilename('attachment; filename="alice \\"work\\".yaml"', "yaml"),
    ).toBe("subscription.yaml");
  });

  it("decodes RFC 5987 extended values", () => {
    const header =
      "attachment; filename*=UTF-8''%E5%95%86%E5%8A%A1-%E6%89%8B%E6%9C%BA.yaml";
    expect(deliveryFilename(header, "yaml")).toBe("商务-手机.yaml");
  });

  it("rejects traversal, control characters and wrong extensions", () => {
    expect(
      deliveryFilename('attachment; filename="../../etc/passwd.yaml"', "yaml"),
    ).toBe("subscription.yaml");
    expect(
      deliveryFilename('attachment; filename="alice.yaml\\n"', "yaml"),
    ).toBe("subscription.yaml");
    expect(deliveryFilename('attachment; filename="alice.txt"', "yaml")).toBe(
      "subscription.yaml",
    );
    expect(deliveryFilename('attachment; filename="alice.yaml"', "link")).toBe(
      "subscription.txt",
    );
  });
});
