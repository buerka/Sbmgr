import { Box, Button, Divider, Stack, Typography } from "@mui/material";
import { Link } from "react-router-dom";
import {
  ActionButton,
  Badge,
  Empty,
  Metric,
  PageHeader,
  Panel,
  UserTable,
} from "../components/common";
import { Icon } from "../components/Icons";
import { bytes, rate } from "../format";
import { useAppSelector } from "../store";
export function Overview() {
  const s = useAppSelector((s) => s.admin.snapshot)!;
  const enabled = s.users.filter((u) => u.status === "已启用").length,
    usage = s.users.reduce((n, u) => n + u.used, 0),
    down = s.users.reduce((n, u) => n + u.current_down, 0),
    up = s.users.reduce((n, u) => n + u.current_up, 0),
    active = s.users.reduce((n, u) => n + u.connections.length, 0);
  return (
    <>
      <PageHeader
        title="运行总览"
        description="查看当前用量、用户状态与管理任务。"
        actions={<ActionButton id="user.add" variant="contained" />}
      />
      <Box className="metrics">
        <Metric
          label="已启用用户"
          value={enabled}
          caption={`共 ${s.users.length} 位用户`}
          icon="users"
        />
        <Metric
          label="本期计费用量"
          value={bytes(usage)}
          caption="按各用户计费方向汇总"
          icon="traffic"
        />
        <Metric
          label="实时下载"
          value={rate(down)}
          caption={`上传 ${rate(up)}`}
        />
        <Metric
          label="活动连接"
          value={active}
          caption={`${s.routes.length} 条主从线路`}
          icon="routes"
        />
      </Box>
      <Panel
        title="用户概览"
        description="当前的管理对象"
        actions={
          <Button component={Link} to="/users" endIcon={<Icon name="next" />}>
            全部用户
          </Button>
        }
      >
        <UserTable users={s.users.slice(0, 5)} />
      </Panel>
      <Box className="two-columns">
        <Panel title="服务器" description="主机统一管理，各入口独立转发">
          <Box px={2.5}>
            {s.members.length ? (
              s.members.map((m) => (
                <Stack
                  key={m.id}
                  className="row-item"
                  direction="row"
                  justifyContent="space-between"
                  alignItems="center"
                >
                  <Box>
                    <Typography variant="body2">{m.id}</Typography>
                    <Typography variant="caption" color="text.secondary">
                      {m.client?.server || m.host || "本机"}
                    </Typography>
                  </Box>
                  <Badge kind={m.master ? "success" : "default"}>
                    {m.master ? "主机" : "从机"}
                  </Badge>
                </Stack>
              ))
            ) : (
              <Stack
                className="row-item"
                direction="row"
                justifyContent="space-between"
              >
                <Box>
                  <Typography variant="body2">本机</Typography>
                  <Typography variant="caption" color="text.secondary">
                    独立管理模式
                  </Typography>
                </Box>
                <Badge>本地</Badge>
              </Stack>
            )}
          </Box>
        </Panel>
        <Panel title="近期告警" description="异常状态与配额提示">
          {s.alerts?.length ? (
            <Box px={2.5}>
              {s.alerts
                .slice(-4)
                .reverse()
                .map((a, i) => (
                  <Box key={i} py={1.7}>
                    <Typography variant="body2">
                      {a.user || "系统"} · {a.kind}
                    </Typography>
                    <Typography variant="caption" color="text.secondary">
                      {a.message}
                    </Typography>
                    {i < 3 && <Divider sx={{ mt: 1.5 }} />}
                  </Box>
                ))}
            </Box>
          ) : (
            <Empty
              title="暂无告警"
              description="当前没有已记录的告警。"
              icon="check"
            />
          )}
        </Panel>
      </Box>
    </>
  );
}
