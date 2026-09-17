import {
  Box,
  ButtonBase,
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
import { Icon, type IconName } from "../components/Icons";
import { bytes, dateTime } from "../format";
import { openAction, useAppDispatch, useAppSelector } from "../store";
const shortcuts: [string, IconName, string][] = [
  ["config.check", "shield", "检查配置与运行环境"],
  ["health.check", "health", "探测本机出站的连通性"],
  ["fleet.check", "server", "获取已登记服务器的状态"],
];
export function Operations() {
  const { snapshot: s, catalog, job } = useAppSelector((s) => s.admin),
    dispatch = useAppDispatch();
  if (!s) return null;
  const health = Object.values(s.health || {});
  return (
    <>
      <PageHeader
        title="运维与备份"
        description="检查服务状态，管理业务数据与恢复记录。"
        actions={<ActionButton id="backup.create" variant="contained" />}
      />
      <Box className="operation-grid">
        {shortcuts.map(([id, icon, description]) => (
          <ButtonBase
            key={id}
            className="operation-tile"
            disabled={job?.status === "running"}
            onClick={() => dispatch(openAction({ id, context: {} }))}
          >
            <Icon name={icon} color="primary" sx={{ fontSize: 25 }} />
            <Box>
              <Typography variant="body2">
                {catalog.find((a) => a.id === id)?.title}
              </Typography>
              <Typography variant="caption" color="text.secondary">
                {description}
              </Typography>
            </Box>
            <Icon name="next" color="disabled" sx={{ ml: "auto" }} />
          </ButtonBase>
        ))}
      </Box>
      <Panel
        title="状态备份"
        description={`${s.backups.length} 份备份 · 恢复后需检查并应用配置。`}
      >
        {s.backups.length ? (
          <DataTable headings={["备份名称", "创建时间", "大小", "操作"]}>
            {s.backups.map((b) => (
              <TableRow key={b.name}>
                <TableCell>
                  <Stack direction="row" alignItems="center" gap={1.2}>
                    <Icon name="backup" color="action" />
                    <Typography variant="body2" className="mono">
                      {b.name}
                    </Typography>
                  </Stack>
                </TableCell>
                <TableCell>{dateTime(b.modified)}</TableCell>
                <TableCell>{bytes(b.size)}</TableCell>
                <TableCell>
                  <ActionButton
                    id="backup.restore"
                    context={{ name: b.name }}
                    variant="text"
                  >
                    恢复
                  </ActionButton>
                </TableCell>
              </TableRow>
            ))}
          </DataTable>
        ) : (
          <Empty
            title="还没有备份"
            description="创建一份备份，保留可恢复的业务状态。"
            icon="backup"
          />
        )}
      </Panel>
      <Box className="two-columns">
        <Panel
          title="出站健康"
          description="端口探测结果；实际代理可用性需端到端验证。"
          actions={
            <ActionButton id="health.set" variant="text">
              检查设置
            </ActionButton>
          }
        >
          {health.length ? (
            <Box px={2.5}>
              {health.map((h) => (
                <Stack
                  className="row-item"
                  key={h.tag}
                  direction="row"
                  justifyContent="space-between"
                  gap={2}
                >
                  <Box>
                    <Typography variant="body2">{h.tag}</Typography>
                    <Typography variant="caption" color="text.secondary">
                      {h.target}
                    </Typography>
                  </Box>
                  <Badge kind={h.healthy ? "success" : "warning"}>
                    {h.healthy ? "探测正常" : "探测异常"}
                  </Badge>
                </Stack>
              ))}
            </Box>
          ) : (
            <Empty
              title="暂无探测记录"
              description="点击上方「检查出站健康」开始。"
              icon="health"
            />
          )}
        </Panel>
        <Panel
          title="监控服务器"
          description="查看远端状态，转发关系在线路页面管理。"
          actions={
            <ActionButton id="fleet.add" variant="text">
              添加服务器
            </ActionButton>
          }
        >
          {s.fleet?.length ? (
            <Box px={2.5}>
              {s.fleet.map((f) => (
                <Stack
                  className="row-item"
                  key={f.name}
                  direction="row"
                  justifyContent="space-between"
                  alignItems="center"
                  gap={1}
                >
                  <Box>
                    <Typography variant="body2">{f.name}</Typography>
                    <Typography variant="caption" color="text.secondary">
                      {f.host} · {dateTime(f.checked)}
                    </Typography>
                  </Box>
                  <Stack direction="row" gap={1}>
                    <Badge kind={f.online ? "success" : "default"}>
                      {f.online ? "在线" : "未知或离线"}
                    </Badge>
                    <ActionButton
                      id="fleet.remove"
                      context={{ name: f.name }}
                      variant="text"
                    >
                      移除
                    </ActionButton>
                  </Stack>
                </Stack>
              ))}
            </Box>
          ) : (
            <Empty
              title="未添加监控服务器"
              description="登记后可在这里查看检查结果。"
              icon="server"
            />
          )}
        </Panel>
      </Box>
      <Panel title="操作审计" description="最近 100 项已完成的管理操作。">
        {s.audit?.length ? (
          <DataTable headings={["时间", "操作者", "操作"]}>
            {s.audit.map((a, i) => (
              <TableRow key={i}>
                <TableCell>{dateTime(a.at)}</TableCell>
                <TableCell>{a.actor}</TableCell>
                <TableCell>{a.action}</TableCell>
              </TableRow>
            ))}
          </DataTable>
        ) : (
          <Empty title="暂无操作记录" />
        )}
      </Panel>
    </>
  );
}
