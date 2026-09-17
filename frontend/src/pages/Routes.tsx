import {
  Alert,
  Box,
  Stack,
  TableCell,
  TableRow,
  Typography,
} from "@mui/material";
import {
  ActionButton,
  Badge,
  DataTable,
  Empty,
  PageHeader,
  Panel,
} from "../components/common";
import { Icon } from "../components/Icons";
import { useAppSelector } from "../store";
export function RoutesPage() {
  const s = useAppSelector((s) => s.admin.snapshot)!;
  return (
    <>
      <PageHeader
        title="线路与服务器"
        description="管理客户端入口、转发路径与最终落地。"
        actions={
          <>
            <ActionButton id="mesh.check" />
            <ActionButton id="mesh.sync" />
            <ActionButton id="mesh.apply" variant="contained" />
          </>
        }
      />
      {s.mesh_pending && (
        <Alert severity="warning" sx={{ mb: 3 }}>
          拓扑有待生效修改或未完成事务。应用拓扑后再分配节点；异常事务可在下方恢复。
        </Alert>
      )}
      <Panel
        title="服务器"
        description={`拓扑修订 ${s.revision}`}
        actions={
          <Stack direction="row" gap={1}>
            {!s.members.length && <ActionButton id="mesh.init" />}
            <ActionButton id="mesh.add" />
          </Stack>
        }
      >
        <Box px={2.5}>
          {s.members.length ? (
            s.members.map((m) => (
              <Stack
                key={m.id}
                className="row-item"
                direction={{ xs: "column", md: "row" }}
                alignItems={{ xs: "flex-start", md: "center" }}
                justifyContent="space-between"
                gap={2}
              >
                <Box>
                  <Stack direction="row" gap={1} alignItems="center">
                    <Icon name="server" color="action" />
                    <Typography variant="body2">{m.id}</Typography>
                    <Badge kind={m.master ? "success" : "default"}>
                      {m.master ? "主机" : "从机"}
                    </Badge>
                  </Stack>
                  <Typography
                    variant="caption"
                    color="text.secondary"
                    display="block"
                    mt={0.8}
                  >
                    {m.client?.server || m.host || "本机"} ·{" "}
                    {m.client ? "可作为客户端入口" : "尚未配置客户端入口"}
                  </Typography>
                </Box>
                <Stack direction="row" gap={1}>
                  <ActionButton
                    id="mesh.entry"
                    context={{
                      id: m.id,
                      server: m.client?.server || "",
                      port: String(m.client?.port || 443),
                      server_name: m.client?.server_name || "",
                      reality_public_key: m.client?.reality_public_key || "",
                      short_id: m.client?.short_id || "",
                    }}
                  >
                    入口设置
                  </ActionButton>
                  {!m.master && (
                    <ActionButton
                      id="mesh.remove"
                      context={{ id: m.id }}
                      color="error"
                      variant="text"
                    >
                      移除
                    </ActionButton>
                  )}
                </Stack>
              </Stack>
            ))
          ) : (
            <Empty
              title="尚未初始化主从管理"
              description="单机用户可直接使用本机出站。"
              icon="server"
            />
          )}
        </Box>
      </Panel>
      <Panel
        title="主从线路"
        description="保存编排后统一应用，再到用户详情中分配。"
        actions={<ActionButton id="mesh.route" />}
      >
        {s.routes.length ? (
          <DataTable headings={["线路", "入口 / 路径 / 落地", "操作"]}>
            {s.routes.map((r) => (
              <TableRow key={r.id}>
                <TableCell>{r.id}</TableCell>
                <TableCell>
                  <Stack direction="row" alignItems="center" gap={1}>
                    <Badge kind="default">{r.entry}</Badge>
                    <Icon name="next" color="disabled" />
                    <Typography variant="body2">
                      {r.hops.join(" → ")}
                    </Typography>
                    <Icon name="next" color="disabled" />
                    <Badge kind="default">{r.exit || "直接出站"}</Badge>
                  </Stack>
                </TableCell>
                <TableCell>
                  <Stack direction="row" gap={1}>
                    <ActionButton
                      id="mesh.route"
                      context={{
                        id: r.id,
                        entry: r.entry,
                        hops: r.hops.join(","),
                        exit: r.exit,
                      }}
                    >
                      编辑
                    </ActionButton>
                    <ActionButton
                      id="mesh.remove-route"
                      context={{ id: r.id }}
                      color="error"
                      variant="text"
                    >
                      删除
                    </ActionButton>
                  </Stack>
                </TableCell>
              </TableRow>
            ))}
          </DataTable>
        ) : (
          <Empty title="尚无主从线路" icon="routes" />
        )}
      </Panel>
      <Panel
        title="本机出站与端点"
        description="第三方落地由独立服务提供，sbmgr 管理接入。"
        actions={<ActionButton id="proxy.add" />}
      >
        {s.proxies.length ? (
          <DataTable headings={["标识", "协议", "类别", "操作"]}>
            {s.proxies.map((p) => (
              <TableRow key={`${p.kind}:${p.tag}`}>
                <TableCell>{p.tag}</TableCell>
                <TableCell>{p.type}</TableCell>
                <TableCell>{p.kind === "endpoint" ? "端点" : "出站"}</TableCell>
                <TableCell>
                  <Stack direction="row" gap={1}>
                    <ActionButton
                      id="proxy.replace"
                      context={{ kind: p.kind, tag: p.tag }}
                    >
                      替换
                    </ActionButton>
                    <ActionButton
                      id="proxy.delete"
                      context={{ kind: p.kind, tag: p.tag }}
                      color="error"
                      variant="text"
                    >
                      删除
                    </ActionButton>
                  </Stack>
                </TableCell>
              </TableRow>
            ))}
          </DataTable>
        ) : (
          <Empty title="没有可管理的出站" icon="routes" />
        )}
      </Panel>
      <ActionButton id="mesh.recover" />
    </>
  );
}
