import { useState } from "react";
import {
  ActionButton,
  ActionMenu,
  PageHeader,
  UserTable,
  userColumns,
  type UserColumn,
} from "../components/common";
import { Icon } from "../components/Icons";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuCheckboxItem,
} from "../components/ui/dropdown-menu";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "../components/ui/select";
import { useAppSelector } from "../store";
export function Users() {
  const users = useAppSelector((s) => s.admin.snapshot)!.users;
  const [search, setSearch] = useState(""),
    [filter, setFilter] = useState("all"),
    [sort, setSort] = useState<"asc" | "desc">("asc"),
    [columns, setColumns] = useState<UserColumn[]>(
      Object.keys(userColumns) as UserColumn[],
    ),
    [page, setPage] = useState(0),
    [size, setSize] = useState(10);
  const enabled = users.filter((u) => u.status === "已启用").length;
  const visible = users
    .filter(
      (u) =>
        u.name.toLowerCase().includes(search.trim().toLowerCase()) &&
        (filter === "all" ||
          (filter === "enabled"
            ? u.status === "已启用"
            : u.status !== "已启用")),
    )
    .sort(
      (a, b) =>
        a.name.localeCompare(b.name, "zh-CN", { numeric: true }) *
        (sort === "asc" ? 1 : -1),
    );
  const pages = Math.max(1, Math.ceil(visible.length / size)),
    safePage = Math.min(page, pages - 1),
    filtered = !!search || filter !== "all";
  return (
    <>
      <PageHeader
        title="用户管理"
        description="管理用户的用量、设备与访问权限。"
        actions={
          <>
            <ActionMenu
              label="更多操作"
              items={[{ id: "user.clone" }, { id: "user.batch" }]}
            />
            <ActionButton id="user.add" variant="default" />
          </>
        }
      />
      <div className="data-toolbar">
        <div className="search-field">
          <Input
            aria-label="搜索用户"
            placeholder="搜索用户…"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setPage(0);
            }}
          />
          {search && (
            <button
              className="clear-search"
              aria-label="清除搜索"
              onClick={() => {
                setSearch("");
                setPage(0);
              }}
            >
              <Icon name="close" />
            </button>
          )}
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="outline"
              size="sm"
              className="border-dashed"
              aria-label="用户状态筛选"
            >
              <Icon name="filter" />
              状态
              {filter !== "all" && (
                <span className="filter-count">
                  {filter === "enabled" ? "已启用" : "需关注"}
                </span>
              )}
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="min-w-48">
            <DropdownMenuLabel>用户状态</DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuRadioGroup
              value={filter}
              onValueChange={(v) => {
                setFilter(v);
                setPage(0);
              }}
            >
              {[
                ["all", "全部用户", users.length],
                ["enabled", "已启用", enabled],
                ["attention", "需关注", users.length - enabled],
              ].map(([v, l, n]) => (
                <DropdownMenuRadioItem key={v} value={String(v)}>
                  {l}
                  <span className="ml-auto pl-8 text-muted-foreground text-xs">
                    {n}
                  </span>
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </DropdownMenuContent>
        </DropdownMenu>
        {filtered && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setSearch("");
              setFilter("all");
              setPage(0);
            }}
          >
            重置
            <Icon name="close" />
          </Button>
        )}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="outline"
              size="sm"
              className="ml-auto"
              aria-label="显示列"
            >
              <Icon name="view" />
              <span>显示列</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuLabel>显示列</DropdownMenuLabel>
            <DropdownMenuSeparator />
            {(Object.entries(userColumns) as [UserColumn, string][]).map(
              ([key, label]) => (
                <DropdownMenuCheckboxItem
                  key={key}
                  checked={columns.includes(key)}
                  onSelect={(e) => e.preventDefault()}
                  onCheckedChange={(checked) =>
                    setColumns((old) =>
                      (Object.keys(userColumns) as UserColumn[]).filter((k) =>
                        k === key ? checked : old.includes(k),
                      ),
                    )
                  }
                >
                  {label}
                </DropdownMenuCheckboxItem>
              ),
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <UserTable
        users={visible.slice(safePage * size, (safePage + 1) * size)}
        columns={columns}
        sort={sort}
        onSort={() => {
          setSort((v) => (v === "asc" ? "desc" : "asc"));
          setPage(0);
        }}
      />
      <div className="pagination">
        <p className="text-sm text-muted-foreground">
          共 {visible.length} 位用户{filtered && ` · 全部 ${users.length} 位`}
        </p>
        <div className="flex items-center gap-5">
          <div className="flex items-center gap-2 text-sm">
            <span className="page-size-label">每页</span>
            <Select
              value={String(size)}
              onValueChange={(v) => {
                setSize(Number(v));
                setPage(0);
              }}
            >
              <SelectTrigger size="sm" aria-label="每页用户数">
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
          <span className="text-sm tabular-nums">
            {safePage + 1} / {pages}
          </span>
          <div className="flex gap-1">
            <Button
              variant="outline"
              size="icon"
              className="pagination-edge h-8 w-8"
              aria-label="第一页"
              disabled={safePage === 0}
              onClick={() => setPage(0)}
            >
              <Icon name="first" />
            </Button>
            <Button
              variant="outline"
              size="icon"
              className="h-8 w-8"
              aria-label="上一页"
              disabled={safePage === 0}
              onClick={() => setPage(safePage - 1)}
            >
              <Icon name="previous" />
            </Button>
            <Button
              variant="outline"
              size="icon"
              className="h-8 w-8"
              aria-label="下一页"
              disabled={safePage + 1 >= pages}
              onClick={() => setPage(safePage + 1)}
            >
              <Icon name="next" />
            </Button>
            <Button
              variant="outline"
              size="icon"
              className="pagination-edge h-8 w-8"
              aria-label="最后一页"
              disabled={safePage + 1 >= pages}
              onClick={() => setPage(pages - 1)}
            >
              <Icon name="last" />
            </Button>
          </div>
        </div>
      </div>
    </>
  );
}
