import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "../api";
import { coverageText } from "../components/MachineTraffic";
import { Badge, Empty, PageHeader } from "../components/common";
import { Icon } from "../components/Icons";
import { memberName } from "../components/routeModel";
import { Button } from "../components/ui/button";
import { Alert } from "../components/ui/feedback";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../components/ui/select";
import { bytes, dateTime } from "../format";
import { useAppSelector } from "../store";
import type {
  MachineTrafficHistory as HistoryResponse,
  MachineTrafficRecord,
} from "../types";

const labels: Record<MachineTrafficRecord["status"], string> = {
  unconfigured: "未设置周期",
  collecting: "部分覆盖",
  complete: "采集完整",
  no_data: "暂无采样",
  error: "采集异常",
  settled: "已结算",
  future: "尚未开始",
};

export function MachineTrafficHistory() {
  const snapshot = useAppSelector((state) => state.admin.snapshot)!;
  const records = snapshot.machine_traffic || [];
  const [params, setParams] = useSearchParams();
  const member = params.get("member") || records[0]?.member || "";
  const [page, setPage] = useState(1);
  const [refreshKey, setRefreshKey] = useState(0);
  const [result, setResult] = useState<HistoryResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!member) return;
    const controller = new AbortController();
    let active = true;
    setLoading(true);
    setResult(null);
    setError("");
    void api
      .machineTrafficHistory(member, page, controller.signal)
      .then((data) => {
        if (active) setResult(data);
      })
      .catch((reason: unknown) => {
        if (active)
          setError(
            reason instanceof Error ? reason.message : "读取历史周期失败",
          );
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      controller.abort();
    };
  }, [member, page, refreshKey]);

  const pageCount = result
    ? Math.max(1, Math.ceil(result.total / Math.max(1, result.page_size)))
    : 1;
  return (
    <div className="space-y-6">
      <div>
        <Link to="/overview" className="back-link">
          <Icon name="back" /> 运行总览
        </Link>
      </div>
      <PageHeader
        title="已结束和已结算的周期"
        description="历史从首次机器流量采样起，按日期倒序分页查看；未采集或跨期的流量未知。机器物理网卡流量与用户配额无关。"
        actions={
          <Button
            variant="outline"
            disabled={loading || !member}
            onClick={() => setRefreshKey((value) => value + 1)}
          >
            <Icon name="refresh" className={loading ? "animate-spin" : ""} />{" "}
            刷新
          </Button>
        }
      />
      <div className="max-w-xs space-y-2">
        <label className="text-sm font-medium" htmlFor="history-member">
          机器
        </label>
        <Select
          value={member || undefined}
          onValueChange={(value) => {
            setParams({ member: value });
            setPage(1);
          }}
        >
          <SelectTrigger id="history-member">
            <SelectValue placeholder="选择机器" />
          </SelectTrigger>
          <SelectContent>
            {records.map((record) => (
              <SelectItem key={record.member} value={record.member}>
                {record.member === "local"
                  ? "本机"
                  : memberName(snapshot, record.member)}
              </SelectItem>
            ))}
            {member && !records.some((record) => record.member === member) && (
              <SelectItem value={member}>{member}</SelectItem>
            )}
          </SelectContent>
        </Select>
      </div>
      {!member && (
        <Empty
          title="暂无受管机器"
          description="添加机器后可查看已采集周期。"
          icon="server"
        />
      )}
      {loading && (
        <p role="status" className="text-sm text-muted-foreground">
          正在读取历史周期…
        </p>
      )}
      {error && (
        <Alert kind="error">
          {error}{" "}
          <Button
            variant="outline"
            size="sm"
            onClick={() => setRefreshKey((value) => value + 1)}
          >
            重试
          </Button>
        </Alert>
      )}
      {result &&
        (result.periods.length ? (
          <>
            <div className="space-y-3">
              {result.periods.map((period, index) => {
                const sampled = period.covered_seconds > 0;
                return (
                  <section
                    key={`${period.period_start}-${period.period_end}-${index}`}
                    className="rounded-xl border bg-card p-4 sm:p-5"
                  >
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <h2 className="font-semibold">
                        {period.period_start} 至 {period.period_end}
                      </h2>
                      <Badge
                        kind={
                          period.status === "complete"
                            ? "success"
                            : period.status === "error"
                              ? "error"
                              : period.status === "collecting"
                                ? "warning"
                                : "default"
                        }
                      >
                        {labels[period.status]}
                      </Badge>
                    </div>
                    <p className="text-xs text-muted-foreground mt-1">
                      结束日包含当日 ·{" "}
                      {period.interval && period.unit
                        ? `每 ${period.interval} ${period.unit === "day" ? "天" : period.unit === "month" ? "个月" : "年"}换期`
                        : "按当期保存的规则"}
                    </p>
                    {(period.effective_start_at || period.effective_end_at) && (
                      <p className="text-xs text-muted-foreground mt-1">
                        {period.effective_start_at &&
                          `规则生效于 ${dateTime(period.effective_start_at)}`}
                        {period.effective_start_at &&
                          period.effective_end_at &&
                          " · "}
                        {period.effective_end_at &&
                          `规则结算于 ${dateTime(period.effective_end_at)}`}
                      </p>
                    )}
                    <dl className="grid grid-cols-1 gap-2 mt-4 sm:grid-cols-3">
                      {(
                        [
                          ["上传", period.upload_bytes],
                          ["下载", period.download_bytes],
                          ["合计", period.total_bytes],
                        ] as const
                      ).map(([label, value]) => (
                        <div key={label} className="rounded-md bg-muted/50 p-3">
                          <dt className="text-xs text-muted-foreground">
                            {label}
                          </dt>
                          <dd className="font-semibold tabular-nums mt-1">
                            {sampled ? bytes(value) : "—"}
                          </dd>
                        </div>
                      ))}
                    </dl>
                    <p className="text-xs text-muted-foreground leading-relaxed mt-3">
                      {coverageText(period)}
                    </p>
                  </section>
                );
              })}
            </div>
            <div className="flex flex-wrap items-center justify-between gap-3 text-sm text-muted-foreground">
              <span>
                第 {result.page} / {pageCount} 页 · 共 {result.total} 个历史周期
              </span>
              <div className="flex gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page <= 1}
                  onClick={() => setPage((value) => value - 1)}
                >
                  上一页
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page >= pageCount}
                  onClick={() => setPage((value) => value + 1)}
                >
                  下一页
                </Button>
              </div>
            </div>
          </>
        ) : (
          <Empty
            title="暂无已结束的周期"
            description="首次采样所属周期结束后，可在这里查看历史。"
            icon="traffic"
          />
        ))}
    </div>
  );
}
