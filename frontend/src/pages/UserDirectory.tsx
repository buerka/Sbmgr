import { useState } from "react";
import { Link } from "react-router-dom";
import { groupsOf } from "../components/groupModel";
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
  const snapshot = useAppSelector((s) => s.admin.snapshot)!;
  const users = snapshot.users;
  const [groupFilter, setGroupFilter] = useState("all");
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
        (groupFilter === "all" || (u.group_id || "default") === groupFilter) &&
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
    filtered = !!search || filter !== "all" || groupFilter !== "all";
  return (
    <>
      <PageHeader
        title="用户管理"
        description="点击用户所在的整行，进入统一配置页管理用量、设备、线路与访问权限。"
        actions={
          <>
            <Button variant="outline" asChild>
              <Link to="/groups">管理分组</Link>
            </Button>
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
        <Select
          value={groupFilter}
          onValueChange={(value) => {
            setGroupFilter(value);
            setPage(0);
          }}
        >
          <SelectTrigger aria-label="用户分组筛选">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部分组</SelectItem>
            {groupsOf(snapshot).map((g) => (
              <SelectItem key={g.id} value={g.id}>
                {g.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
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
              setGroupFilter("all");
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
