import { useState } from "react";
import { Alert, Button, Stack, TableCell, TableRow } from "@mui/material";
import { Link } from "react-router-dom";
import {
  ActionButton,
  Badge,
  DataTable,
  Empty,
  PageHeader,
  Panel,
} from "../components/common";
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
        actions={
          <>
            <ActionButton id="client.set" />
            <ActionButton id="template.set" />
            <ActionButton id="subscription.set" />
          </>
        }
      />
      <Alert severity="info" sx={{ mb: 3 }}>
        订阅服务：{s.subscription.enabled ? "已启用" : "未启用"} · 模板：
        {s.subscription.template ? "自定义 Mihomo 模板" : "简易配置"}
        {s.subscription.base_url ? ` · ${s.subscription.base_url}` : ""}
      </Alert>
      <Panel
        title="设备订阅"
        description="停用、到期或超额的设备会被拒绝交付。"
      >
        {devices.length ? (
          <DataTable headings={["用户", "设备", "状态", "节点", "交付与管理"]}>
            {devices.map(({ u, d }) => (
              <TableRow key={`${u.name}/${d.name}`}>
                <TableCell>
                  <Button
                    component={Link}
                    to={`/users/${encodeURIComponent(u.name)}`}
                  >
                    {u.name}
                  </Button>
                </TableCell>
                <TableCell>{d.name}</TableCell>
                <TableCell>
                  <Badge kind={d.deliverable ? "success" : "default"}>
                    {d.deliverable ? "可交付" : "已停用"}
                  </Badge>
                </TableCell>
                <TableCell>
                  {u.nodes.filter((n) => n.device === d.name).length} 个
                </TableCell>
                <TableCell>
                  <Stack direction="row" gap={1}>
                    <Button
                      variant="outlined"
                      disabled={Boolean(busy)}
                      onClick={() =>
                        void download({ user: u.name, device: d.name }, "link")
                      }
                    >
                      下载订阅地址
                    </Button>
                    <Button
                      variant="outlined"
                      disabled={Boolean(busy)}
                      onClick={() =>
                        void download({ user: u.name, device: d.name }, "yaml")
                      }
                    >
                      下载 YAML
                    </Button>
                    <ActionButton
                      id="device.rotate-link"
                      context={{ user: u.name, device: d.name }}
                    />
                  </Stack>
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
