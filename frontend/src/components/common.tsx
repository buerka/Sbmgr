import { Fragment, useRef, type ComponentProps, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { Icon, type IconName } from "./Icons";
import { Button } from "./ui/button";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "./ui/dropdown-menu";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui/table";
import { Progress } from "./ui/feedback";
import { openAction, useAppDispatch, useAppSelector } from "../store";
import { bytes, rate } from "../format";
import { cn } from "../lib/utils";
import type { Context, User } from "../types";
import { rememberActionTrigger } from "./actionFocus";
const actionIcons: Record<string, IconName> = {
  "user.add": "add",
  "device.add": "add",
  "node.add": "add",
  "node.assign": "routes",
  "mesh.add": "add",
  "mesh.route": "routes",
  "proxy.add": "add",
  "backup.create": "backup",
  "config.check": "shield",
  "health.check": "health",
  "fleet.check": "server",
  "user.clone": "copy",
  "user.set": "edit",
  "mesh.sync": "refresh",
  "mesh.check": "check",
};
export function ActionButton({
  id,
  context = {},
  children,
  variant = "outline",
  ...props
}: Omit<ComponentProps<typeof Button>, "id"> & {
  id: string;
  context?: Context;
}) {
  const dispatch = useAppDispatch();
  const { catalog, job, snapshot } = useAppSelector((s) => s.admin);
  const action = catalog.find((a) => a.id === id);
  return (
    <Button
      variant={variant}
      {...props}
      disabled={
        props.disabled ||
        !action ||
        job?.status === "running" ||
        (snapshot?.role === "slave" && /^(user|device|node)\./.test(id))
      }
      onClick={(event) => {
        rememberActionTrigger(event.currentTarget);
        dispatch(openAction({ id, context }));
      }}
    >
      {actionIcons[id] && <Icon name={actionIcons[id]} />}{" "}
      {children || action?.title || id}
    </Button>
  );
}
export interface MenuAction {
  id: string;
  context?: Context;
  label?: string;
  divider?: boolean;
}
export function ActionMenu({
  label = "更多操作",
  items,
  icon = "more",
  compact = false,
}: {
  label?: string;
  items: MenuAction[];
  icon?: IconName;
  compact?: boolean;
}) {
  const dispatch = useAppDispatch(),
    { catalog, job, snapshot } = useAppSelector((s) => s.admin);
  const openingDialog = useRef(false);
  const trigger = useRef<HTMLButtonElement>(null);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant={compact ? "ghost" : "outline"}
          size={compact ? "icon" : "default"}
          className={compact ? "h-8 w-8" : undefined}
          ref={trigger}
          aria-label={label}
        >
          <Icon name={icon} />
          {!compact && (
            <>
              {label}
              <Icon name="chevron" />
            </>
          )}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        aria-label={label}
        align="end"
        className="min-w-48"
        onCloseAutoFocus={(e) => {
          if (openingDialog.current) {
            e.preventDefault();
            openingDialog.current = false;
          }
        }}
      >
        {items.map((item) => {
          const action = catalog.find((a) => a.id === item.id);
          return (
            <Fragment key={item.id}>
              <DropdownMenuItem
                variant={action?.danger ? "destructive" : "default"}
                disabled={
                  !action ||
                  job?.status === "running" ||
                  (snapshot?.role === "slave" &&
                    /^(user|device|node)\./.test(item.id))
                }
                onSelect={() => {
                  openingDialog.current = true;
                  rememberActionTrigger(trigger.current);
                  dispatch(
                    openAction({ id: item.id, context: item.context || {} }),
                  );
                }}
              >
                <Icon
                  name={
                    actionIcons[item.id] ||
                    (action?.danger ? "warning" : "settings")
                  }
                />
                {item.label || action?.title || item.id}
              </DropdownMenuItem>
              {item.divider && <DropdownMenuSeparator />}
            </Fragment>
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
export function PageHeader({
  title,
  description,
  actions,
}: {
  title: ReactNode;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="page-heading">
      <div>
        <h1>{title}</h1>
        {description && (
          <p className="text-muted-foreground mt-1">{description}</p>
        )}
      </div>
      {actions && (
        <div className="flex flex-wrap items-center gap-2">{actions}</div>
      )}
    </div>
  );
}
export function Panel({
  title,
  description,
  actions,
  children,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="panel">
      <div className="panel-heading">
        <div>
          <h2>{title}</h2>
          {description && (
            <p className="text-sm text-muted-foreground mt-1">{description}</p>
          )}
        </div>
        {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
      </div>
      {children}
    </section>
  );
}
export function Empty({
  title,
  description,
  icon = "document",
}: {
  title: string;
  description?: string;
  icon?: IconName;
}) {
  return (
    <div className="empty">
      <div className="empty-icon">
        <Icon name={icon} size={22} />
      </div>
      <p className="font-medium">{title}</p>
      {description && (
        <p className="text-sm text-muted-foreground max-w-sm">{description}</p>
      )}
    </div>
  );
}
export function Metric({
  label,
  value,
  caption,
  icon = "download",
}: {
  label: string;
  value: ReactNode;
  caption: string;
  icon?: IconName;
}) {
  return (
    <section className="metric">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-medium">{label}</h2>
        <Icon name={icon} className="text-muted-foreground" />
      </div>
      <p className="metric-value">{value}</p>
      <p className="text-xs text-muted-foreground">{caption}</p>
    </section>
  );
}
export function Badge({
  children,
  kind = "success",
}: {
  children: ReactNode;
  kind?: "success" | "warning" | "default" | "error";
}) {
  return (
    <span className={cn("status-badge", `status-${kind}`)}>{children}</span>
  );
}
export function DataTable({
  headings,
  children,
}: {
  headings: ReactNode[];
  children: ReactNode;
}) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          {headings.map((h, i) => (
            <TableHead key={i}>{h}</TableHead>
          ))}
        </TableRow>
      </TableHeader>
      <TableBody>{children}</TableBody>
    </Table>
  );
}
export const userColumns = {
  status: "状态",
  usage: "本期用量 / 配额",
  speed: "实时速率",
  devices: "设备 / 节点",
  expires: "到期时间",
};
export type UserColumn = keyof typeof userColumns;
export function UserTable({
  users,
  columns = Object.keys(userColumns) as UserColumn[],
  sort,
  onSort,
}: {
  users: User[];
  columns?: UserColumn[];
  sort?: "asc" | "desc";
  onSort?: () => void;
}) {
  return (
    <div className="table-frame">
      <DataTable
        headings={[
          onSort ? (
            <button
              className="sort-button"
              onClick={onSort}
              aria-label={`按用户名${sort === "asc" ? "降序" : "升序"}排列`}
            >
              用户
              <Icon name={sort === "asc" ? "up" : "down"} />
            </button>
          ) : (
            "用户"
          ),
          ...columns.map((c) => userColumns[c]),
          <span className="sr-only">操作</span>,
        ]}
      >
        {!users.length && (
          <TableRow>
            <TableCell colSpan={columns.length + 2}>
              <Empty
                title="没有符合条件的用户"
                description="试试其他名称或状态，或创建第一个用户。"
                icon="users"
              />
            </TableCell>
          </TableRow>
        )}
        {users.map((u) => (
          <TableRow key={u.name}>
            <TableCell>
              <Link
                className="font-medium hover:underline underline-offset-4"
                to={`/users/${encodeURIComponent(u.name)}`}
              >
                {u.name}
              </Link>
            </TableCell>
            {columns.map((col) => (
              <TableCell key={col}>
                {col === "status" ? (
                  <Badge
                    kind={
                      u.status === "已启用"
                        ? "success"
                        : u.status === "已禁用"
                          ? "default"
                          : "warning"
                    }
                  >
                    {u.status}
                  </Badge>
                ) : col === "usage" ? (
                  <div className="usage-cell">
                    <div className="flex justify-between gap-4 text-xs">
                      <span>{bytes(u.used)}</span>
                      <span className="text-muted-foreground">
                        {u.quota ? bytes(u.quota + u.extra_quota) : "不限"}
                      </span>
                    </div>
                    <Progress
                      label={`${u.name} 配额使用率`}
                      value={
                        u.quota ? (u.used / (u.quota + u.extra_quota)) * 100 : 0
                      }
                    />
                  </div>
                ) : col === "speed" ? (
                  <div className="flex items-center gap-3 tabular-nums">
                    <span className="inline-flex items-center gap-1">
                      <Icon
                        name="down"
                        size={12}
                        className="text-muted-foreground"
                      />
                      {rate(u.current_down)}
                    </span>
                    <span className="inline-flex items-center gap-1 text-muted-foreground">
                      <Icon name="up" size={12} />
                      {rate(u.current_up)}
                    </span>
                  </div>
                ) : col === "devices" ? (
                  <span className="text-muted-foreground">
                    {u.devices.length} 台 / {u.nodes.length} 个
                  </span>
                ) : (
                  <span className="text-muted-foreground">
                    {u.expires || "长期有效"}
                  </span>
                )}
              </TableCell>
            ))}
            <TableCell className="w-10">
              <div className="flex items-center gap-1">
                <ActionButton
                  id="node.assign"
                  context={{ user: u.name }}
                  size="sm"
                  variant="ghost"
                  aria-label={`分配线路：${u.name}`}
                >
                  分配线路
                </ActionButton>
                <ActionMenu
                  label={`管理用户：${u.name}`}
                  compact
                  items={[
                    { id: "user.set", context: { user: u.name } },
                    { id: "user.ip", context: { user: u.name } },
                    {
                      id: "user.access",
                      context: { user: u.name },
                      divider: true,
                    },
                    {
                      id: u.enabled ? "user.disable" : "user.enable",
                      context: { user: u.name },
                    },
                    { id: "user.delete", context: { user: u.name } },
                  ]}
                />
              </div>
            </TableCell>
          </TableRow>
        ))}
      </DataTable>
    </div>
  );
}
