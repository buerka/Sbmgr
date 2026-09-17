import { useId, useState, type ReactNode } from "react";
import {
  Box,
  Button,
  Chip,
  Divider,
  ListItemIcon,
  ListItemText,
  Menu,
  MenuItem,
  Paper,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Typography,
  type ButtonProps,
} from "@mui/material";
import { Link } from "react-router-dom";
import { Icon, type IconName } from "./Icons";
import { openAction, useAppDispatch, useAppSelector } from "../store";
import { bytes, rate } from "../format";
import type { Context, User } from "../types";

const actionIcons: Record<string, IconName> = {
  "user.add": "add",
  "device.add": "add",
  "node.add": "add",
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
  ...props
}: Omit<ButtonProps, "id"> & { id: string; context?: Context }) {
  const dispatch = useAppDispatch();
  const { catalog, job } = useAppSelector((s) => s.admin);
  const action = catalog.find((a) => a.id === id);
  return (
    <Button
      variant="outlined"
      disabled={!action || job?.status === "running"}
      startIcon={actionIcons[id] ? <Icon name={actionIcons[id]} /> : undefined}
      onClick={() => dispatch(openAction({ id, context }))}
      {...props}
    >
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
}: {
  label?: string;
  items: MenuAction[];
  icon?: IconName;
}) {
  const [anchor, setAnchor] = useState<HTMLElement | null>(null),
    id = useId();
  const dispatch = useAppDispatch(),
    { catalog, job } = useAppSelector((s) => s.admin);
  return (
    <>
      <Button
        variant="outlined"
        color="inherit"
        startIcon={<Icon name={icon} />}
        endIcon={<Icon name="chevron" />}
        aria-haspopup="menu"
        aria-controls={anchor ? id : undefined}
        aria-expanded={Boolean(anchor)}
        onClick={(e) => setAnchor(e.currentTarget)}
      >
        {label}
      </Button>
      <Menu
        id={id}
        anchorEl={anchor}
        open={Boolean(anchor)}
        onClose={() => setAnchor(null)}
        MenuListProps={{ "aria-label": label }}
      >
        {items.map((item) => {
          const action = catalog.find((a) => a.id === item.id);
          return (
            <MenuItem
              key={item.id}
              disabled={!action || job?.status === "running"}
              divider={item.divider}
              sx={action?.danger ? { color: "error.main" } : undefined}
              onClick={() => {
                setAnchor(null);
                dispatch(
                  openAction({ id: item.id, context: item.context || {} }),
                );
              }}
            >
              <ListItemIcon sx={{ color: "inherit" }}>
                <Icon
                  name={
                    actionIcons[item.id] ||
                    (action?.danger ? "warning" : "settings")
                  }
                />
              </ListItemIcon>
              <ListItemText>
                {item.label || action?.title || item.id}
              </ListItemText>
            </MenuItem>
          );
        })}
      </Menu>
    </>
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
    <Stack
      className="page-heading"
      direction={{ xs: "column", md: "row" }}
      justifyContent="space-between"
      gap={2}
    >
      <Box>
        <Typography variant="h1">{title}</Typography>
        {description && (
          <Typography variant="body2" color="text.secondary" mt={0.8}>
            {description}
          </Typography>
        )}
      </Box>
      {actions && (
        <Stack direction="row" gap={1} flexWrap="wrap" alignItems="flex-start">
          {actions}
        </Stack>
      )}
    </Stack>
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
    <Paper component="section" variant="outlined" className="panel">
      <Stack
        className="panel-heading"
        direction="row"
        justifyContent="space-between"
        alignItems="center"
        gap={2}
        flexWrap="wrap"
      >
        <Box>
          <Typography variant="h2">{title}</Typography>
          {description && (
            <Typography
              variant="caption"
              color="text.secondary"
              display="block"
              mt={0.5}
            >
              {description}
            </Typography>
          )}
        </Box>
        {actions}
      </Stack>
      <Divider />
      {children}
    </Paper>
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
    <Stack className="empty" alignItems="center" gap={0.8}>
      <Icon name={icon} sx={{ fontSize: 30, color: "#9bafbf", mb: 0.5 }} />
      <Typography variant="body2" color="text.secondary">
        {title}
      </Typography>
      {description && (
        <Typography variant="caption" color="text.secondary">
          {description}
        </Typography>
      )}
    </Stack>
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
    <Paper variant="outlined" className="metric">
      <Stack direction="row" justifyContent="space-between" gap={1}>
        <Typography variant="body2" color="text.secondary">
          {label}
        </Typography>
        <Icon name={icon} sx={{ color: "#7798b5" }} />
      </Stack>
      <Typography className="metric-value">{value}</Typography>
      <Typography variant="caption" color="text.secondary">
        {caption}
      </Typography>
    </Paper>
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
    <Chip
      label={children}
      color={kind}
      variant="filled"
      sx={
        kind === "success"
          ? { bgcolor: "#eaf5ef", color: "#2e7650" }
          : kind === "warning"
            ? { bgcolor: "#fff3df", color: "#91601b" }
            : undefined
      }
    />
  );
}
export function DataTable({
  headings,
  children,
}: {
  headings: string[];
  children: ReactNode;
}) {
  return (
    <TableContainer>
      <Table size="small">
        <TableHead>
          <TableRow>
            {headings.map((h, i) => (
              <TableCell key={i}>{h}</TableCell>
            ))}
          </TableRow>
        </TableHead>
        <TableBody>{children}</TableBody>
      </Table>
    </TableContainer>
  );
}
export function UserTable({ users }: { users: User[] }) {
  if (!users.length)
    return (
      <Empty
        title="没有符合条件的用户"
        description="试试其他名称或状态，或创建第一个用户。"
        icon="users"
      />
    );
  return (
    <DataTable
      headings={[
        "用户",
        "状态",
        "本期用量 / 配额",
        "实时速率",
        "设备 / 节点",
        "到期时间",
        "",
      ]}
    >
      {users.map((u) => (
        <TableRow key={u.name} hover>
          <TableCell>
            <Stack direction="row" gap={1.4} alignItems="center">
              <Box className="user-avatar">
                <Icon name="users" />
              </Box>
              <Box>
                <Button
                  component={Link}
                  to={`/users/${encodeURIComponent(u.name)}`}
                  className="user-link"
                >
                  {u.name}
                </Button>
                <Typography component="span" className="cell-caption">
                  {{
                    total: "双向计费",
                    upload: "上传计费",
                    download: "下载计费",
                  }[u.quota_mode] || "双向计费"}
                </Typography>
              </Box>
            </Stack>
          </TableCell>
          <TableCell>
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
          </TableCell>
          <TableCell>
            {bytes(u.used)}{" "}
            <Box component="span" color="text.secondary">
              / {u.quota ? bytes(u.quota + u.extra_quota) : "不限"}
            </Box>
            <Typography component="span" className="cell-caption">
              上传 {bytes(u.upload)} · 下载 {bytes(u.download)}
            </Typography>
          </TableCell>
          <TableCell>
            {rate(u.current_down)}
            <Typography component="span" className="cell-caption">
              上传 {rate(u.current_up)}
            </Typography>
          </TableCell>
          <TableCell>
            {u.devices.length} 台 / {u.nodes.length} 个
          </TableCell>
          <TableCell>{u.expires || "长期有效"}</TableCell>
          <TableCell>
            <Button
              component={Link}
              to={`/users/${encodeURIComponent(u.name)}`}
              endIcon={<Icon name="next" />}
            >
              管理
            </Button>
          </TableCell>
        </TableRow>
      ))}
    </DataTable>
  );
}
