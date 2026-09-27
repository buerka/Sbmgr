import { ResetSubscriptionLink } from "./ResetSubscriptionLink";
import { useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import { notify, useAppDispatch, useAppSelector } from "../store";
import type { Context, User, Device } from "../types";
import { Icon } from "./Icons";
import { ActionMenu, Badge, DataTable, Empty, Panel } from "./common";
import { Button } from "./ui/button";
import { TableCell, TableRow } from "./ui/table";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "./ui/dropdown-menu";

type DeliveryUser = Pick<User, "name" | "status"> & {
  devices: Device[];
  nodes: { device: string }[];
};
export function DeviceDelivery({
  user,
  readOnly = false,
  enabled,
  selfService,
}: {
  user?: DeliveryUser;
  readOnly?: boolean;
  enabled?: boolean;
  selfService?: { version: string; refresh: () => Promise<void> };
}) {
  const adminSubscriptionEnabled = useAppSelector(
    (s) => s.admin.snapshot?.subscription.enabled,
  );
  const subscriptionEnabled = enabled ?? adminSubscriptionEnabled;
  const users = useAppSelector((s) => s.admin.snapshot?.users) || [];
  const dispatch = useAppDispatch();
  const [busy, setBusy] = useState("");
  const devices = (user ? [user] : users).flatMap((u) =>
    u.devices.map((d) => ({ u, d })),
  );

  async function deliver(context: Context, format: "copy" | "link" | "yaml") {
    setBusy(`${context.user}/${context.device}`);
    try {
      if (format === "copy") await api.copySubscriptionLink(context);
      else await api.delivery(context, format);
      dispatch(
        notify({
          message:
            format === "copy"
              ? "订阅链接已复制。"
              : "交付文件已下载，请妥善保存。",
          severity: "success",
        }),
      );
    } catch (error) {
      dispatch(
        notify({
          message: error instanceof Error ? error.message : "交付失败",
          severity: "error",
        }),
      );
    } finally {
      setBusy("");
    }
  }

  return (
    <Panel title="设备订阅" description="停用、到期或超额的设备会被拒绝交付。">
      {devices.length ? (
        <DataTable
          headings={
            user
              ? ["设备", "状态", "节点", readOnly ? "获取订阅" : "交付与管理"]
              : ["用户", "设备", "状态", "节点", "交付与管理"]
          }
        >
          {devices.map(({ u, d }) => (
            <TableRow key={`${u.name}/${d.name}`}>
              {!user && (
                <TableCell>
                  <Link
                    className="font-medium hover:underline underline-offset-4"
                    to={`/users/${encodeURIComponent(u.name)}`}
                  >
                    {u.name}
                  </Link>
                </TableCell>
              )}
              <TableCell>{d.label || d.name}</TableCell>
              <TableCell>
                <Badge kind={d.deliverable ? "success" : "default"}>
                  {d.deliverable
                    ? "可交付"
                    : !d.enabled
                      ? "设备已停用"
                      : u.status !== "已启用"
                        ? u.status
                        : "暂不可交付"}
                </Badge>
              </TableCell>
              <TableCell>
                {u.nodes.filter((n) => n.device === d.name).length} 个
              </TableCell>
              <TableCell>
                <div className="flex gap-2">
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={
                          !!busy || !d.deliverable || !subscriptionEnabled
                        }
                      >
                        <Icon
                          name={
                            busy === `${u.name}/${d.name}` ? "loading" : "link"
                          }
                          className={
                            busy === `${u.name}/${d.name}`
                              ? "animate-spin"
                              : undefined
                          }
                        />
                        订阅地址 <Icon name="chevron" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem
                        disabled={
                          !!busy || !d.deliverable || !subscriptionEnabled
                        }
                        onSelect={() =>
                          void deliver({ user: u.name, device: d.name }, "copy")
                        }
                      >
                        <Icon name="copy" />
                        复制链接
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        disabled={
                          !!busy || !d.deliverable || !subscriptionEnabled
                        }
                        onSelect={() =>
                          void deliver({ user: u.name, device: d.name }, "link")
                        }
                      >
                        <Icon name="download" />
                        下载 TXT
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!!busy || !d.deliverable}
                    onClick={() =>
                      void deliver({ user: u.name, device: d.name }, "yaml")
                    }
                  >
                    <Icon name="download" />
                    YAML
                  </Button>
                  {readOnly && selfService && (
                    <ResetSubscriptionLink
                      device={d}
                      version={selfService.version}
                      refresh={selfService.refresh}
                    />
                  )}
                  {!readOnly && (
                    <ActionMenu
                      compact
                      label={`管理订阅：${u.name}/${d.name}`}
                      items={[
                        {
                          id: "device.rotate-link",
                          context: { user: u.name, device: d.name },
                        },
                      ]}
                    />
                  )}
                </div>
              </TableCell>
            </TableRow>
          ))}
        </DataTable>
      ) : (
        <Empty
          title="暂无设备"
          description={
            user ? "先为此用户创建设备并分配节点。" : "先创建用户并分配节点。"
          }
          icon="device"
        />
      )}
    </Panel>
  );
}
