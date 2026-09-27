import { Link } from "react-router-dom";
import { ActionButton, Badge, Empty, Panel } from "./common";
import { Button } from "./ui/button";
import { Icon } from "./Icons";
import { memberName } from "./routeModel";
import { bytes } from "../format";
import type { MachineTrafficRecord, Snapshot } from "../types";

const statusLabels: Record<MachineTrafficRecord["status"], string> = {
  unconfigured: "未设置周期",
  collecting: "采集中",
  complete: "采集完整",
  no_data: "暂无数据",
  error: "采集异常",
  settled: "已结算",
  future: "尚未开始",
};

function localTime(value: string) {
  if (!value) return "";
  const time = new Date(value);
  return Number.isNaN(time.getTime())
    ? value
    : time.toLocaleString("zh-CN", { hour12: false });
}

export function coverageText(record: MachineTrafficRecord) {
  if (record.future || record.status === "future")
    return "首期尚未开始；开始前的流量不属于本周期，也不能补采。";
  if (record.status === "unconfigured")
    return "后台持续采集网卡流量，设置续费周期后显示该时段的用量。";
  if (record.status === "error")
    return record.note || "采集失败，请检查机器连接和网卡状态。";
  if (record.status === "no_data" || record.covered_seconds <= 0)
    return (
      record.note ||
      "该周期尚无采样。历史流量无法补采，不能将当前数字视为整期用量。"
    );
  const coverage = Number.isFinite(record.coverage_percent)
    ? Math.min(100, Math.max(0, record.coverage_percent))
    : 0;
  const sampled = [
    localTime(record.first_sample_at),
    localTime(record.last_sample_at),
  ].filter(Boolean);
  const range = sampled.length ? ` · 采样 ${sampled.join(" 至 ")}` : "";
  return `已覆盖该周期约 ${coverage.toFixed(1)}%${range}。${record.note ? `${record.note} ` : ""}${
    record.status === "complete"
      ? ""
      : "未覆盖的时间不能推算，数字仅代表已采集流量。"
  }`;
}

export function MachineTraffic({ snapshot }: { snapshot: Snapshot }) {
  const records = snapshot.machine_traffic;
  return (
    <Panel
      title="机器流量"
      description="按每台机器自动换期的续费周期统计物理网卡流量；包含非代理流量，与用户配额无关。历史周期在详情页按需读取。"
    >
      {!records ? (
        <Empty
          title="机器流量暂不可用"
          description="刷新页面后重试；新采样功能启用前的历史流量无法补采。"
          icon="traffic"
        />
      ) : records.length === 0 ? (
        <Empty
          title="暂无受管机器"
          description="添加机器后，可设置其续费周期并查看流量。"
          icon="server"
        />
      ) : (
        <div className="grid gap-4 p-4 sm:p-6">
          {records.map((record) => {
            const hasSample = record.covered_seconds > 0;
            return (
              <section
                key={record.member}
                className="rounded-lg border border-border p-4 sm:p-5"
                aria-label={`${record.member === "local" ? "本机" : memberName(snapshot, record.member)} 机器流量`}
              >
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <h3 className="break-all text-sm font-semibold">
                        {record.member === "local"
                          ? "本机"
                          : memberName(snapshot, record.member)}
                      </h3>
                      <Badge
                        kind={
                          record.status === "complete"
                            ? "success"
                            : record.status === "error"
                              ? "error"
                              : record.status === "collecting"
                                ? "warning"
                                : "default"
                        }
                      >
                        {statusLabels[record.status]}
                      </Badge>
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {record.period_start && record.period_end
                        ? record.future
                          ? `首期将于 ${record.period_start} 开始`
                          : `本周期：${record.period_start} 至 ${record.period_end}（含结束日）`
                        : "尚未设置续费周期"}
                    </p>
                    {record.interval && record.unit && (
                      <p className="mt-1 text-xs text-muted-foreground">
                        每 {record.interval}{" "}
                        {record.unit === "day"
                          ? "天"
                          : record.unit === "month"
                            ? "个月"
                            : "年"}
                        自动换期
                        {record.next_reset
                          ? ` · 下次换期 ${record.next_reset}`
                          : ""}
                        {record.future ? " · 等待首期开启" : ""}
                      </p>
                    )}
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <Button asChild variant="outline" size="sm">
                      <Link
                        to={`/ops/machine-traffic?member=${encodeURIComponent(record.member)}`}
                      >
                        历史周期 <Icon name="next" />
                      </Link>
                    </Button>
                    <ActionButton
                      id="machine.traffic_period"
                      disabled={snapshot.role === "slave"}
                      context={{
                        member: record.member,
                        start: record.anchor_start || record.period_start || "",
                        interval: String(record.interval || 1),
                        unit: record.unit || "month",
                      }}
                      size="sm"
                    >
                      {record.anchor_start ? "修改周期" : "设置周期"}
                    </ActionButton>
                  </div>
                </div>
                <dl className="mt-4 grid gap-3 sm:grid-cols-3">
                  {(
                    [
                      ["上传", record.upload_bytes],
                      ["下载", record.download_bytes],
                      ["合计", record.total_bytes],
                    ] as const
                  ).map(([label, value]) => (
                    <div key={label} className="rounded-md bg-muted/50 p-3">
                      <dt className="text-xs text-muted-foreground">{label}</dt>
                      <dd className="mt-1 text-lg font-semibold tabular-nums">
                        {hasSample ? bytes(value) : "—"}
                      </dd>
                    </div>
                  ))}
                </dl>
                <p className="mt-3 text-xs leading-relaxed text-muted-foreground">
                  {coverageText(record)}
                </p>
              </section>
            );
          })}
        </div>
      )}
    </Panel>
  );
}
