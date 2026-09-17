import { useState } from "react";
import { Link } from "react-router-dom";
import {
  ActionButton,
  ActionMenu,
  Badge,
  DataTable,
  Empty,
  PageHeader,
  Panel,
} from "../components/common";
import { Button } from "../components/ui/button";
import { TableCell, TableRow } from "../components/ui/table";
import { Icon } from "../components/Icons";
import { api } from "../api";
import { notify, useAppDispatch, useAppSelector } from "../store";
import type { Context } from "../types";
export function Subscriptions() {
  const s = useAppSelector((s) => s.admin.snapshot)!,
    dispatch = useAppDispatch(),
    [busy, setBusy] = useState("");
  const devices = s.users.flatMap((u) => u.devices.map((d) => ({ u, d })));
  async function download(context: Context, format: string) {
    setBusy(`${context.user}/${context.device}`);
    try {
      await api.delivery(context, format);
      dispatch(
        notify({
          message: "交付文件已下载，请妥善保存。",
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
    <>
      <PageHeader
        title="订阅交付"
        description="每台设备独立授权，按需下载订阅。"
      />
      <div className="subscription-summary">
        <section className="setting-card">
          <div className="flex justify-between items-center">
            <h2>订阅服务</h2>
            <ActionButton id="subscription.set" variant="ghost" size="sm">
              编辑
            </ActionButton>
          </div>
          <div className="mt-4">
            <Badge kind={s.subscription.enabled ? "success" : "default"}>
              {s.subscription.enabled ? "已启用" : "未启用"}
            </Badge>
          </div>
          <p className="text-sm text-muted-foreground mt-3 break-all">
            {s.subscription.base_url || "尚未配置公开地址"}
          </p>
        </section>
        <section className="setting-card">
          <div className="flex justify-between items-center">
            <h2>客户端入口</h2>
            <ActionButton id="client.set" variant="ghost" size="sm">
              编辑
            </ActionButton>
          </div>
          <p className="font-medium text-sm mt-4 break-all">
            {s.client.server
              ? `${s.client.server}:${s.client.port}`
              : "尚未配置"}
          </p>
          <p className="text-xs text-muted-foreground mt-2">
            本机直出节点的连接地址
          </p>
        </section>
        <section className="setting-card">
          <div className="flex justify-between items-center">
            <h2>客户端模板</h2>
            <ActionButton id="template.set" variant="ghost" size="sm">
              编辑
            </ActionButton>
          </div>
          <p className="font-medium text-sm mt-4">
            {s.subscription.template ? "自定义 Mihomo 模板" : "简易配置"}
          </p>
          <p className="text-xs text-muted-foreground mt-2 break-all">
            {s.subscription.template_path || "使用默认规则与分组"}
          </p>
        </section>
      </div>
      <Panel
        title="设备订阅"
        description="停用、到期或超额的设备会被拒绝交付。"
      >
        {devices.length ? (
          <DataTable headings={["用户", "设备", "状态", "节点", "交付与管理"]}>
            {devices.map(({ u, d }) => (
              <TableRow key={`${u.name}/${d.name}`}>
                <TableCell>
                  <Link
                    className="font-medium hover:underline underline-offset-4"
                    to={`/users/${encodeURIComponent(u.name)}`}
                  >
                    {u.name}
                  </Link>
                </TableCell>
                <TableCell>{d.name}</TableCell>
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
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={
                        !!busy || !d.deliverable || !s.subscription.enabled
                      }
                      onClick={() =>
                        void download({ user: u.name, device: d.name }, "link")
                      }
                    >
                      <Icon name="link" />
                      订阅地址
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={!!busy || !d.deliverable}
                      onClick={() =>
                        void download({ user: u.name, device: d.name }, "yaml")
                      }
                    >
                      <Icon name="download" />
                      YAML
                    </Button>
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
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </DataTable>
        ) : (
          <Empty
            title="暂无设备"
            description="先创建用户并分配节点。"
            icon="device"
          />
        )}
      </Panel>
    </>
  );
}
