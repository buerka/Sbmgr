import {
  Box,
  Button,
  Stack,
  TableCell,
  TableRow,
  Typography,
} from "@mui/material";
import { Link, useParams } from "react-router-dom";
import {
  ActionButton,
  ActionMenu,
  Badge,
  DataTable,
  Empty,
  Metric,
  PageHeader,
  Panel,
} from "../components/common";
import { Icon } from "../components/Icons";
import { bytes, rate } from "../format";
import { useAppSelector } from "../store";
export function UserDetail() {
  const { name } = useParams();
  const u = useAppSelector((s) => s.admin.snapshot)!.users.find(
    (u) => u.name === name,
  );
  if (!u)
    return (
      <Empty
        title="用户不存在"
        description="请返回用户列表重新选择。"
        icon="users"
      />
    );
  const context = { user: u.name };
  return (
    <>
      <PageHeader
        title={u.name}
        description="用户详情 · 保存策略后，在顶部统一应用。"
        actions={
          <Button component={Link} to="/users" startIcon={<Icon name="back" />}>
            返回用户
          </Button>
        }
      />
      <Box className="metrics">
        <Metric
          label="当前状态"
          value={u.status}
          caption={u.expires ? `到期 ${u.expires}` : "长期有效"}
          icon="users"
        />
        <Metric
          label="本期用量"
          value={bytes(u.used)}
          caption={
            u.quota ? `配额 ${bytes(u.quota + u.extra_quota)}` : "流量不限"
          }
          icon="traffic"
        />
        <Metric
          label="实时下载"
          value={rate(u.current_down)}
          caption={`上传 ${rate(u.current_up)}`}
        />
        <Metric
          label="已授权节点"
          value={u.nodes.length}
          caption={`${u.devices.length} 台设备`}
          icon="routes"
        />
      </Box>
      <Stack direction="row" gap={1} flexWrap="wrap" mb={3}>
        <ActionButton id="user.set" context={context} />
        <ActionMenu
          label="访问与保护"
          icon="shield"
          items={["user.ip", "user.access", "user.burst", "user.throttle"].map(
            (id) => ({ id, context }),
          )}
        />
        <ActionButton id="device.add" context={context} />
        <ActionButton id="node.add" context={context} />
        <ActionMenu
          items={[
            { id: u.enabled ? "user.disable" : "user.enable", context },
            { id: "user.unblock", context },
            { id: "user.reset", context, divider: true },
            { id: "user.delete", context },
          ]}
        />
      </Stack>
      <Panel
        title="设备与节点"
        description="按设备分配线路，分别管理授权与用量。"
      >
        {u.devices.map((d) => {
          const dc = { ...context, device: d.name },
            nodes = u.nodes.filter((n) => n.device === d.name);
          return (
            <Box className="device-card" key={d.name}>
              <Stack
                direction={{ xs: "column", md: "row" }}
                justifyContent="space-between"
                alignItems={{ xs: "flex-start", md: "center" }}
                gap={2}
                mb={2}
              >
                <Stack direction="row" alignItems="center" gap={1}>
                  <Icon name="device" color="action" />
                  <Typography variant="h3">{d.name}</Typography>
                  <Badge kind={d.enabled ? "success" : "default"}>
                    {d.enabled ? "已启用" : "已禁用"}
                  </Badge>
                </Stack>
                <Stack direction="row" gap={1}>
                  <ActionButton id="node.add" context={dc} />
                  <ActionMenu
                    label="设备设置"
                    icon="settings"
                    items={[
                      "device.ip",
                      "device.access",
                      d.enabled ? "device.disable" : "device.enable",
                      "device.rotate-link",
                      "device.rotate",
                      "device.delete",
                    ].map((id) => ({ id, context: dc }))}
                  />
                </Stack>
              </Stack>
              {nodes.length ? (
                <Box className="node-grid">
                  {nodes.map((n) => (
                    <Box className="node-card" key={n.name}>
                      <Stack
                        direction="row"
                        alignItems="flex-start"
                        justifyContent="space-between"
                        gap={1}
                      >
                        <Typography variant="h3">{n.name}</Typography>
                        <Badge kind="default">{n.entry}</Badge>
                      </Stack>
                      <Typography
                        variant="caption"
                        color="text.secondary"
                        display="block"
                        mt={1.5}
                      >
                        {n.outbound || "默认直出"}
                        <br />
                        上传上限 {n.up_mbps ? `${n.up_mbps} Mbps` : "不限"} ·
                        下载上限 {n.down_mbps ? `${n.down_mbps} Mbps` : "不限"}
                      </Typography>
                      <Stack direction="row" gap={4} my={2}>
                        <Box>
                          <Typography variant="caption" color="text.secondary">
                            上传
                          </Typography>
                          <Typography>{bytes(n.upload)}</Typography>
                        </Box>
                        <Box>
                          <Typography variant="caption" color="text.secondary">
                            下载
                          </Typography>
                          <Typography>{bytes(n.download)}</Typography>
                        </Box>
                      </Stack>
                      <Stack direction="row" gap={1} flexWrap="wrap">
                        <ActionButton
                          id="node.set"
                          context={{ ...dc, node: n.name }}
                        >
                          名称与限速
                        </ActionButton>
                        <ActionButton
                          id="node.delete"
                          context={{ ...dc, node: n.name }}
                          color="error"
                          variant="text"
                        >
                          撤销
                        </ActionButton>
                      </Stack>
                    </Box>
                  ))}
                </Box>
              ) : (
                <Empty title="尚未分配节点" icon="routes" />
              )}
            </Box>
          );
        })}
      </Panel>
      <Panel title="活动连接" description="最多展示最近 50 项。">
        {u.connections.length ? (
          <DataTable headings={["设备 / 节点", "来源", "目标", "开始时间"]}>
            {u.connections.slice(0, 50).map((c, i) => (
              <TableRow key={i}>
                <TableCell>
                  {c.device} / {c.node}
                </TableCell>
                <TableCell>{c.source}</TableCell>
                <TableCell>{c.target}</TableCell>
                <TableCell>{c.since}</TableCell>
              </TableRow>
            ))}
          </DataTable>
        ) : (
          <Empty title="当前没有记录到活动连接" icon="link" />
        )}
      </Panel>
      <Panel title="最近访问" description="最多展示最近 100 项。">
        {u.accesses?.length ? (
          <DataTable headings={["目标", "设备 / 节点", "次数", "最后访问"]}>
            {u.accesses.map((a, i) => (
              <TableRow key={i}>
                <TableCell>{a.target}</TableCell>
                <TableCell>
                  {a.device} / {a.node}
                </TableCell>
                <TableCell>{a.count}</TableCell>
                <TableCell>{a.last_seen}</TableCell>
              </TableRow>
            ))}
          </DataTable>
        ) : (
          <Empty title="暂无访问记录" />
        )}
      </Panel>
    </>
  );
}
