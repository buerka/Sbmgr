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
