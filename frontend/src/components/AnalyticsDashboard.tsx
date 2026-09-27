import { useEffect, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api";
import { bytes, dateTime } from "../format";
import type { AnalyticsSnapshot } from "../types";
import { DataTable, Empty, Metric, Panel } from "./common";
import { Icon } from "./Icons";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Alert } from "./ui/feedback";
import { TableCell, TableRow } from "./ui/table";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./ui/select";

type Days = 1 | 7 | 30;
type Sort = "traffic" | "connections";
const ALL = "__all__";

function Trend({ rows }: { rows: AnalyticsSnapshot["series"] }) {
  const max = Math.max(1, ...rows.map((row) => row.upload + row.download));
  const width = 720;
  const slot = width / Math.max(rows.length, 1);
  return (
    <div
      className="overflow-x-auto"
      role="img"
      aria-label="每日上传与下载流量趋势"
    >
      <svg
        viewBox="0 0 720 180"
        className="w-full min-w-[360px] h-44"
        aria-hidden="true"
      >
        <line
          x1="0"
          y1="146"
          x2="720"
          y2="146"
          stroke="currentColor"
          opacity="0.2"
        />
        {rows.map((row, index) => {
          const x = index * slot + slot * 0.2;
          const bar = slot * 0.6;
          const up = (row.upload / max) * 120;
          const down = (row.download / max) * 120;
          return (
            <g key={`${row.date}-${index}`}>
              <title>{`${row.date} UTC 采集日：上传 ${bytes(row.upload)}，下载 ${bytes(row.download)}，${row.connections} 次连接`}</title>
              <rect
                x={x}
                y={146 - down}
                width={bar}
                height={down}
                rx="2"
                fill="currentColor"
                className="text-primary"
              />
              <rect
                x={x}
                y={146 - down - up}
                width={bar}
                height={up}
                rx="2"
                fill="currentColor"
                className="text-primary/40"
              />
              {(rows.length <= 7 ||
                index % 5 === 0 ||
                index === rows.length - 1) && (
                <text
                  x={x + bar / 2}
                  y="168"
                  textAnchor="middle"
                  className="fill-muted-foreground text-[11px]"
                >
                  {row.date.slice(5)}
                </text>
              )}
            </g>
          );
        })}
      </svg>
      <p className="text-xs text-muted-foreground">
        深色为下载，浅色为上传。按收到流量增量的 UTC
        采集日计入；断线跨日的实际发生日无法回填。
      </p>
    </div>
  );
}

export function AnalyticsDashboard({
  user,
  devices,
}: {
  user?: string;
  devices: { name: string; label?: string }[];
}) {
  const [params, setParams] = useSearchParams();
  const device = params.get("device") || "";
  const [days, setDays] = useState<Days>(7);
  const [sort, setSort] = useState<Sort>("traffic");
  const [searchText, setSearchText] = useState("");
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [data, setData] = useState<AnalyticsSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [refreshKey, setRefreshKey] = useState(0);

  function chooseDevice(value: string) {
    const next = new URLSearchParams(params);
    if (value === ALL) next.delete("device");
    else next.set("device", value);
    setParams(next);
    setPage(1);
  }

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    setLoading(true);
    setData(null);
    setError("");
    void api
      .analytics(
        { user, device: device || undefined, days, page, search, sort },
        controller.signal,
      )
      .then((reply) => {
        if (active) setData(reply);
      })
      .catch((reason: unknown) => {
        if (active)
          setError(
            reason instanceof Error ? reason.message : "读取数据看板失败",
          );
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      controller.abort();
    };
  }, [user, device, days, page, search, sort, refreshKey]);

  const hasTraffic = Boolean(
    data &&
    (data.totals.upload || data.totals.download || data.totals.connections),
  );
  const coverage = data?.coverage;
  const coverageMessage = coverage
    ? coverage.status === "unavailable"
      ? "采集暂不可用，当前范围不能视为完整统计。"
      : coverage.status === "partial"
        ? "当前范围只覆盖部分时间，缺口期间的流量未计入。"
        : "正在采集；开始采集前的流量无法补录。"
    : "";
  const pageCount = data
    ? Math.max(
        1,
        Math.ceil(
          data.pagination.total / Math.max(1, data.pagination.page_size),
        ),
      )
    : 1;

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap gap-3 items-end justify-between">
        <div>
          <h2 className="text-xl font-semibold">网站数据看板</h2>
          <p className="text-sm text-muted-foreground mt-1">
            按域名或目标 IP
            汇总已采集的实际字节；次数指连接次数，不代表页面浏览量。
          </p>
        </div>
        <Button
          variant="outline"
          onClick={() => setRefreshKey((value) => value + 1)}
          disabled={loading}
        >
          <Icon name="refresh" className={loading ? "animate-spin" : ""} />
          刷新
        </Button>
      </div>
      <div className="flex flex-wrap gap-3 items-end">
        <div className="space-y-1 min-w-40">
          <label className="text-sm" htmlFor="analytics-device">
            设备
          </label>
          <Select value={device || ALL} onValueChange={chooseDevice}>
            <SelectTrigger id="analytics-device">
              <SelectValue placeholder="全部设备" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>全部设备</SelectItem>
              {devices.map((item) => (
                <SelectItem key={item.name} value={item.name}>
                  {item.label || item.name}
                </SelectItem>
              ))}
              {device && !devices.some((item) => item.name === device) && (
                <SelectItem value={device}>{device}</SelectItem>
              )}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1 min-w-32">
          <label className="text-sm" htmlFor="analytics-days">
            时间范围
          </label>
          <Select
            value={String(days)}
            onValueChange={(value) => {
              setDays(Number(value) as Days);
              setPage(1);
            }}
          >
            <SelectTrigger id="analytics-days">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="1">近 1 日</SelectItem>
              <SelectItem value="7">近 7 日</SelectItem>
              <SelectItem value="30">近 30 日</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1 min-w-40">
          <label className="text-sm" htmlFor="analytics-sort">
            网站排序
          </label>
          <Select
            value={sort}
            onValueChange={(value) => {
              setSort(value as Sort);
              setPage(1);
            }}
          >
            <SelectTrigger id="analytics-sort">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="traffic">按流量</SelectItem>
              <SelectItem value="connections">按连接次数</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <form
          className="flex gap-2 items-end flex-1 min-w-52"
          onSubmit={(event: FormEvent) => {
            event.preventDefault();
            setSearch(searchText.trim());
            setPage(1);
          }}
        >
          <div className="space-y-1 flex-1">
            <label className="text-sm" htmlFor="analytics-search">
              网站筛选
            </label>
            <Input
              id="analytics-search"
              value={searchText}
              onChange={(event) => setSearchText(event.target.value)}
              placeholder="域名或目标 IP"
            />
          </div>
          <Button type="submit" variant="outline">
            查询
          </Button>
        </form>
      </div>
      {loading && (
        <p role="status" className="text-sm text-muted-foreground">
          正在读取已采集数据…
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
      {data && (
        <>
          <Alert
            kind={
              coverage?.status === "partial" ||
              coverage?.status === "unavailable"
                ? "warning"
                : undefined
            }
          >
            <strong>{coverageMessage}</strong>{" "}
            {coverage?.note && <span>{coverage.note} </span>}
            {coverage?.first_seen && (
              <span>首次采集 {dateTime(coverage.first_seen)}。 </span>
            )}
            {coverage?.gaps ? (
              <span>记录缺口 {coverage.gaps} 处。 </span>
            ) : null}
            <span>
              范围 {data.from.slice(0, 10)} 至 {data.to.slice(0, 10)}（UTC
              采集日）。
            </span>
          </Alert>
          {!hasTraffic && coverage?.status !== "unavailable" && (
            <Empty
              title="等待采集数据"
              description="当前时间范围暂无已采集的连接流量。空白时段不代表真实流量为零。"
            />
          )}
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <Metric
              label="已采集下载"
              value={hasTraffic ? bytes(data.totals.download) : "—"}
              caption="网站连接的实际字节"
              icon="download"
            />
            <Metric
              label="已采集上传"
              value={hasTraffic ? bytes(data.totals.upload) : "—"}
              caption="网站连接的实际字节"
              icon="up"
            />
            <Metric
              label="连接次数"
              value={
                hasTraffic
                  ? data.totals.connections.toLocaleString("zh-CN")
                  : "—"
              }
              caption="不是页面浏览量"
              icon="routes"
            />
            <Metric
              label="目标数"
              value={
                hasTraffic ? data.totals.domains.toLocaleString("zh-CN") : "—"
              }
              caption="域名或目标 IP"
              icon="health"
            />
          </div>
          {hasTraffic && data.series.length > 0 && (
            <Panel
              title="每日趋势"
              description="按采集日期显示上传与下载；缺口不补算。"
            >
              <Trend rows={data.series} />
            </Panel>
          )}
          <Panel
            title="设备汇总"
            description="设备间共用用户配额；汇总所选时间范围内各设备已采集的网站连接，可点选设备查看详情。"
          >
            {data.devices.length ? (
              <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                {data.devices.map((item) => (
                  <button
                    type="button"
                    key={item.name}
                    onClick={() => chooseDevice(item.name)}
                    className="rounded-lg border p-4 text-left hover:bg-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                  >
                    <span className="font-medium break-all">
                      {item.label || item.name}
                    </span>
                    <span className="block text-sm text-muted-foreground mt-2">
                      ↓ {bytes(item.download)} · ↑ {bytes(item.upload)}
                    </span>
                    <span className="block text-xs text-muted-foreground mt-1">
                      {item.connections} 次连接 · 最近{" "}
                      {item.last_seen ? dateTime(item.last_seen) : "暂无"}
                    </span>
                  </button>
                ))}
              </div>
            ) : (
              <Empty
                title="暂无设备统计"
                description="等待设备产生可采集的网站连接。"
              />
            )}
          </Panel>
          <Panel
            title="网站排行"
            description="仅展示域名或目标 IP，不包含 HTTPS 路径。"
          >
            {data.domains.length ? (
              <>
                <DataTable
                  headings={[
                    "域名 / 目标 IP",
                    "上传",
                    "下载",
                    "合计",
                    "连接次数",
                    "最近连接",
                  ]}
                >
                  {data.domains.map((item) => (
                    <TableRow key={item.domain}>
                      <TableCell className="font-medium break-all">
                        {item.domain}
                      </TableCell>
                      <TableCell>{bytes(item.upload)}</TableCell>
                      <TableCell>{bytes(item.download)}</TableCell>
                      <TableCell>
                        {bytes(item.upload + item.download)}
                      </TableCell>
                      <TableCell>{item.connections}</TableCell>
                      <TableCell>
                        {item.last_seen ? dateTime(item.last_seen) : "—"}
                      </TableCell>
                    </TableRow>
                  ))}
                </DataTable>
                <div className="flex items-center justify-between gap-3 mt-4 text-sm text-muted-foreground">
                  <span>
                    第 {data.pagination.page} / {pageCount} 页 · 共{" "}
                    {data.pagination.total} 个目标
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
                title={search ? "没有匹配的网站" : "暂无网站统计"}
                description={
                  search
                    ? "请调整域名或目标 IP 筛选。"
                    : "等待采集连接流量后显示。"
                }
              />
            )}
          </Panel>
          <details className="rounded-xl border bg-card p-5 sm:p-6">
            <summary className="cursor-pointer font-medium focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
              最近访问记录
            </summary>
            <p className="text-sm text-muted-foreground my-3">
              每行是一条已采集连接，次数不代表页面浏览量。
            </p>
            {data.recent.length ? (
              <DataTable
                headings={[
                  "域名 / 目标 IP",
                  "设备",
                  "开始",
                  "结束",
                  "上传",
                  "下载",
                  "状态",
                ]}
              >
                {data.recent.map((item, index) => (
                  <TableRow key={`${item.device}-${item.started_at}-${index}`}>
                    <TableCell className="break-all">{item.domain}</TableCell>
                    <TableCell>{item.label || item.device}</TableCell>
                    <TableCell>{dateTime(item.started_at)}</TableCell>
                    <TableCell>
                      {item.closed_at
                        ? dateTime(item.closed_at)
                        : item.status === "active"
                          ? "进行中"
                          : "未获取结束时间"}
                    </TableCell>
                    <TableCell>{bytes(item.upload)}</TableCell>
                    <TableCell>{bytes(item.download)}</TableCell>
                    <TableCell>
                      {item.status === "closed"
                        ? "已结束"
                        : item.status === "active"
                          ? "连接中"
                          : "状态未知"}
                    </TableCell>
                  </TableRow>
                ))}
              </DataTable>
            ) : (
              <Empty title="暂无最近连接" />
            )}
          </details>
        </>
      )}
    </div>
  );
}
