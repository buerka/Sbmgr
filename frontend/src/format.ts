export function bytes(value: number = 0) {
  if (!value) return "0 B";
  const i = Math.min(4, Math.floor(Math.log(Math.abs(value)) / Math.log(1024)));
  return `${(value / 1024 ** i).toFixed(i ? 1 : 0)} ${["B", "KiB", "MiB", "GiB", "TiB"][i]}`;
}
export const rate = (value: number = 0) => `${value.toFixed(2)} Mbps`;
export const dateTime = (value: string) =>
  value ? new Date(value).toLocaleString("zh-CN") : "尚未检查";
export const inputSize = (value: number) =>
  !value ? "0" : value % 2 ** 30 === 0 ? `${value / 2 ** 30}G` : `${value}B`;

export function userNodeSpeed(
  user: import("./types").User,
  direction: "up" | "down",
): number | undefined {
  const key = direction === "up" ? "up_mbps" : "down_mbps";
  if (user[key]) return user[key];
  if (!user.nodes.length) return 0;
  const first = user.nodes[0][key];
  return user.nodes.every((n) => n[key] === first) ? first : undefined;
}
