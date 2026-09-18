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
import { Button } from "../components/ui/button";
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
        actions={<ActionButton id="user.add" variant="default" />}
      />
      <div className="metrics">
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
      </div>
      <Panel
        title="用户概览"
        description="当前的管理对象"
        actions={
          <Button asChild variant="ghost" size="sm">
            <Link to="/users">
              全部用户
              <Icon name="next" />
            </Link>
          </Button>
        }
      >
        <UserTable users={s.users.slice(0, 5)} />
      </Panel>
      <div className="two-columns">
        <Panel title="服务器" description="主机统一管理，各入口独立转发">
          <div className="px-6">
            {s.members.length ? (
              s.members.map((m) => (
                <div key={m.id} className="row-item">
                  <div className="flex items-center gap-3">
                    <span className="surface-icon">
                      <Icon name="server" />
                    </span>
                    <div>
                      <p className="font-medium text-sm">{m.id}</p>
                      <p className="text-xs text-muted-foreground mt-1">
                        {m.client?.server || m.host || "本机"}
                      </p>
                    </div>
                  </div>
                  <Badge kind={m.master ? "success" : "default"}>
                    {m.master ? "主机" : "从机"}
                  </Badge>
                </div>
              ))
            ) : (
              <div className="row-item">
                <div>
                  <p className="text-sm font-medium">本机</p>
                  <p className="text-xs text-muted-foreground mt-1">
                    独立管理模式
                  </p>
                </div>
                <Badge kind="default">本地</Badge>
              </div>
            )}
          </div>
        </Panel>
        <Panel title="近期告警" description="异常状态与配额提示">
          {s.alerts?.length ? (
            <div className="px-6">
              {s.alerts
                .slice(-4)
                .reverse()
                .map((a, i) => (
                  <div className="row-item" key={i}>
                    <div>
                      <p className="text-sm font-medium">
                        {a.user || "系统"} · {a.kind}
                      </p>
                      <p className="text-xs text-muted-foreground mt-1">
                        {a.message}
                      </p>
                    </div>
                  </div>
                ))}
            </div>
          ) : (
            <Empty
              title="暂无告警"
              description="当前没有已记录的告警。"
              icon="check"
            />
          )}
        </Panel>
      </div>
    </>
  );
}
