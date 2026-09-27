export function deliveryFilename(
  header: string | null,
  format: "link" | "yaml",
) {
  const fallback = format === "yaml" ? "subscription.yaml" : "subscription.txt";
  if (!header) return fallback;
  const extended = header.match(/(?:^|;)\s*filename\*\s*=\s*([^;]+)/i);
  const plain = header.match(
    /(?:^|;)\s*filename\s*=\s*(?:"((?:\\.|[^"])*)"|([^;]+))/i,
  );
  let candidate = "";
  if (extended) {
    const value = extended[1].trim().replace(/^"|"$/g, "");
    const encoded = value.match(/^utf-8'[^']*'(.*)$/i);
    if (encoded) {
      try {
        candidate = decodeURIComponent(encoded[1]);
      } catch {
        // An invalid extended value may still have a usable plain fallback.
      }
    }
  }
  if (!candidate && plain)
    candidate = (plain[1] || plain[2] || "").trim().replace(/\\(["\\])/g, "$1");
  const expectedExtension = format === "yaml" ? ".yaml" : ".txt";
  if (
    !candidate ||
    candidate.length > 180 ||
    !candidate.toLowerCase().endsWith(expectedExtension) ||
    /[\\/:*?"<>|\x00-\x1f\x7f]/.test(candidate) ||
    candidate === "." ||
    candidate === ".." ||
    candidate.trim() !== candidate
  )
    return fallback;
  return candidate;
}
