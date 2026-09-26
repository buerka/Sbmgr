import type { ReactNode } from "react";
import { Link, useSearchParams } from "react-router-dom";
import {
  ActionButton,
  DataTable,
  Empty,
  PageHeader,
} from "../components/common";
import { Icon } from "../components/Icons";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../components/ui/select";
import { TableCell, TableRow } from "../components/ui/table";
import { bytes, dateTime } from "../format";
import { useAppSelector } from "../store";

function RecordList<T>({
  title,
  description,
  items,
  searchText,
  time,
  headings,
  row,
  actions,
  empty,
  overview,
}: {
  title: string;
  description: string;
  items: T[];
  searchText: (item: T) => string;
  time: (item: T) => string;
  headings: string[];
  row: (item: T, index: number) => ReactNode;
  actions?: ReactNode;
  empty: string;
  overview?: ReactNode;
}) {
  const [params, setParams] = useSearchParams();
  const query = params.get("q") || "";
  const requestedSize = Number(params.get("size"));
  const size = [10, 25, 50].includes(requestedSize) ? requestedSize : 10;
  const requestedPage = Number(params.get("page") || 1);
  const filtered = items
    .filter((item) =>
      searchText(item).toLowerCase().includes(query.trim().toLowerCase()),
    )
    .sort((a, b) => (Date.parse(time(b)) || 0) - (Date.parse(time(a)) || 0));
  const pages = Math.max(1, Math.ceil(filtered.length / size));
  const page = Math.min(
    pages,
    Math.max(1, Number.isInteger(requestedPage) ? requestedPage : 1),
  );
  const start = (page - 1) * size;
  function update(values: Record<string, string>) {
    const next = new URLSearchParams(params);
    for (const [key, value] of Object.entries(values)) {
      if (value) next.set(key, value);
      else next.delete(key);
    }
    setParams(next, { replace: true });
  }
  return (
    <>
      <Link to="/ops" className="back-link">
        <Icon name="back" />
        系统运维
      </Link>
      <PageHeader title={title} description={description} actions={actions} />
      {overview}
      <div className="data-toolbar">
        <div className="search-field">
          <Input
            aria-label={`搜索${title}`}
            placeholder={
              title === "状态备份"
                ? "搜索备份名称、时间…"
                : "搜索操作者、操作、时间…"
            }
            value={query}
            onChange={(e) => update({ q: e.target.value, page: "" })}
          />
          {query && (
            <button
              className="clear-search"
              aria-label="清除搜索"
              onClick={() => update({ q: "", page: "" })}
            >
              <Icon name="close" />
            </button>
          )}
        </div>
        <span className="text-xs text-muted-foreground ml-auto">
          按时间从新到旧
        </span>
      </div>
      <div className="table-frame">
        {filtered.length ? (
          <DataTable headings={headings}>
            {filtered
              .slice(start, start + size)
              .map((item, index) => row(item, start + index))}
          </DataTable>
        ) : (
          <Empty
            title={items.length ? "没有匹配的记录" : empty}
            description={
              items.length ? "试试其他关键词，或清除搜索。" : undefined
            }
          />
        )}
      </div>
      <nav className="pagination" aria-label={`${title}分页`}>
        <p className="text-sm text-muted-foreground" aria-live="polite">
          共 {filtered.length} 条{query.trim() && ` · 全部 ${items.length} 条`}
          {filtered.length > 0 &&
            ` · 显示 ${start + 1}–${Math.min(start + size, filtered.length)} 条`}
        </p>
        <div className="flex items-center gap-5">
          <div className="flex items-center gap-2 text-sm">
            <span>每页</span>
            <Select
              value={String(size)}
              onValueChange={(value) => update({ size: value, page: "" })}
            >
              <SelectTrigger size="sm" aria-label="每页记录数">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {[10, 25, 50].map((n) => (
                  <SelectItem value={String(n)} key={n}>
                    {n}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <span className="text-sm tabular-nums whitespace-nowrap">
            {page} / {pages}
          </span>
          <div className="flex gap-1">
            <Button
              variant="outline"
              size="icon"
              className="pagination-edge h-8 w-8"
              aria-label="第一页"
              disabled={page === 1}
              onClick={() => update({ page: "" })}
            >
              <Icon name="first" />
            </Button>
            <Button
              variant="outline"
              size="icon"
              className="h-8 w-8"
              aria-label="上一页"
              disabled={page === 1}
              onClick={() => update({ page: String(page - 1) })}
            >
              <Icon name="previous" />
            </Button>
            <Button
              variant="outline"
              size="icon"
              className="h-8 w-8"
              aria-label="下一页"
              disabled={page === pages}
              onClick={() => update({ page: String(page + 1) })}
            >
              <Icon name="next" />
            </Button>
            <Button
              variant="outline"
              size="icon"
              className="pagination-edge h-8 w-8"
              aria-label="最后一页"
              disabled={page === pages}
              onClick={() => update({ page: String(pages) })}
            >
              <Icon name="last" />
            </Button>
          </div>
        </div>
      </nav>
    </>
  );
}

export function Backups() {
  const snapshot = useAppSelector((s) => s.admin.snapshot!);
  const backups = snapshot.backups;
  const stateBytes =
    snapshot.backup_storage?.state_bytes ??
    backups.reduce((sum, b) => sum + b.size, 0);
  const totalBytes = snapshot.backup_storage?.total_bytes ?? stateBytes;
  const days = snapshot.backup_settings?.retention_days || 0;
  return (
    <RecordList
      title="状态备份"
      description="管理本机备份空间和保留期限。恢复业务状态后，需检查并应用配置。"
      overview={
        <div className="two-columns mb-6">
          <section className="rounded-xl border p-5" aria-label="备份空间">
            <p className="text-sm text-muted-foreground">备份总大小</p>
            <p className="text-2xl font-semibold tabular-nums mt-2">
              {bytes(totalBytes)}
            </p>
            <p className="text-sm text-muted-foreground mt-2">
              状态备份 {backups.length} 份 · {bytes(stateBytes)}
              {snapshot.backup_storage &&
                `；其他恢复资料 ${bytes(snapshot.backup_storage.other_bytes)}`}
            </p>
            <p className="text-xs text-muted-foreground mt-2">
              整个备份目录内的文件大小合计，包含配置与部署恢复副本；不随下方搜索改变。
            </p>
          </section>
          <section className="rounded-xl border p-5" aria-label="自动清理">
            <div className="flex items-center justify-between gap-3">
              <h2 className="text-sm font-medium">自动清理</h2>
              <ActionButton id="backup.retention" size="sm" variant="outline">
                设置保留期限
              </ActionButton>
            </div>
            <p className="text-lg font-semibold mt-3">
              {days ? `保留 ${days} 天` : "未启用按时间清理"}
            </p>
            <p className="text-xs text-muted-foreground mt-2 leading-relaxed">
              开启后，后台维护会删除到期的手动和每日状态备份，每类至少保留最新一份。恢复保护和迁移留存不自动删除。
            </p>
            <p className="text-xs text-muted-foreground mt-2">
              不限制备份份数；设置后无需应用配置。
            </p>
          </section>
        </div>
      }
      items={backups}
      time={(b) => b.modified}
      searchText={(b) => `${b.name} ${b.modified} ${dateTime(b.modified)}`}
      headings={["备份名称", "修改时间", "文件大小", "自动清理", "操作"]}
      actions={<ActionButton id="backup.create" variant="default" />}
      empty="还没有备份"
      row={(b) => (
        <TableRow key={b.name}>
          <TableCell>
            <span className="inline-flex items-center gap-2">
              <Icon name="backup" className="text-muted-foreground shrink-0" />
              {b.name}
            </span>
          </TableCell>
          <TableCell>{dateTime(b.modified)}</TableCell>
          <TableCell>{bytes(b.size)}</TableCell>
          <TableCell>
            {b.protected ? (
              <span className="text-muted-foreground">{b.protected}</span>
            ) : b.expires ? (
              <div>
                <p>{dateTime(b.expires)}</p>
                <p className="text-xs text-muted-foreground mt-1">
                  {Date.parse(b.expires) <= Date.parse(snapshot.time)
                    ? "已到期，等待后台清理"
                    : "到期后由后台清理"}
                </p>
              </div>
            ) : (
              "未设置到期清理"
            )}
          </TableCell>
          <TableCell>
            <ActionButton
              id="backup.restore"
              context={{ name: b.name }}
              variant="ghost"
              size="sm"
            >
              恢复
            </ActionButton>
          </TableCell>
        </TableRow>
      )}
    />
  );
}

export function Audit() {
  const audit = useAppSelector((s) => s.admin.snapshot!.audit || []);
  return (
    <RecordList
      title="操作审计"
      description="查看最近 100 项已完成的管理操作；搜索和分页均在这些记录内进行。"
      items={audit}
      time={(a) => a.at}
      searchText={(a) => `${a.actor} ${a.action} ${a.at} ${dateTime(a.at)}`}
      headings={["时间", "操作者", "操作"]}
      empty="暂无操作记录"
      row={(a, index) => (
        <TableRow key={`${a.at}-${index}`}>
          <TableCell>{dateTime(a.at)}</TableCell>
          <TableCell>{a.actor}</TableCell>
          <TableCell>{a.action}</TableCell>
        </TableRow>
      )}
    />
  );
}
