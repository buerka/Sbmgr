import {
  ActionButton,
  Badge,
  Empty,
  PageHeader,
  Panel,
} from "../components/common";
import { Link } from "react-router-dom";
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
        title="系统运维"
        description="检查服务状态，管理业务数据与恢复记录。"
        actions={<ActionButton id="backup.create" variant="default" />}
      />
      <div className="operation-grid">
        {shortcuts.map(([id, icon, description]) => (
          <button
            key={id}
            className="operation-tile"
            disabled={
              job?.status === "running" || !catalog.some((a) => a.id === id)
            }
            onClick={() => dispatch(openAction({ id, context: {} }))}
          >
            <span className="surface-icon">
              <Icon name={icon} size={22} />
            </span>
            <div>
              <p className="font-medium text-sm">
                {catalog.find((a) => a.id === id)?.title}
              </p>
              <p className="text-xs text-muted-foreground mt-1">
                {description}
              </p>
            </div>
            <Icon name="next" className="ml-auto text-muted-foreground" />
          </button>
        ))}
      </div>
      <div className="two-columns mb-6">
        <Link
          to="/ops/backups"
          className="operation-tile"
          aria-label="查看状态备份"
        >
          <span className="surface-icon">
            <Icon name="backup" size={22} />
          </span>
          <div className="min-w-0">
            <h2>
              状态备份{" "}
              <span className="text-sm font-normal text-muted-foreground ml-2">
                {s.backups.length} 份状态备份
              </span>
            </h2>
            <p className="text-xs text-muted-foreground mt-1">
              总大小{" "}
              {bytes(
                s.backup_storage?.total_bytes ??
                  s.backups.reduce((sum, b) => sum + b.size, 0),
              )}{" "}
              · 查看与管理保留期限
            </p>
          </div>
          <Icon
            name="next"
            className="ml-auto shrink-0 text-muted-foreground"
          />
        </Link>
        <Link
          to="/ops/audit"
          className="operation-tile"
          aria-label="查看操作审计"
        >
          <span className="surface-icon">
            <Icon name="document" size={22} />
          </span>
          <div className="min-w-0">
            <h2>
              操作审计{" "}
              <span className="text-sm font-normal text-muted-foreground ml-2">
                最近 {s.audit?.length || 0} 条
              </span>
            </h2>
            <p className="text-xs text-muted-foreground mt-1">
              按操作者、操作或时间查找记录
            </p>
          </div>
          <Icon
            name="next"
            className="ml-auto shrink-0 text-muted-foreground"
          />
        </Link>
      </div>
      <div className="two-columns">
        <Panel
          title="出站健康"
          description="端口探测结果；实际代理可用性需端到端验证。"
          actions={
            <ActionButton id="health.set" variant="ghost" size="sm">
              检查设置
            </ActionButton>
          }
        >
          {health.length ? (
            <div className="px-6">
              {health.map((h) => (
                <div className="row-item" key={h.tag}>
                  <div className="min-w-0">
                    <p className="text-sm font-medium">{h.tag}</p>
                    <p className="text-xs text-muted-foreground mt-1 break-all">
                      {h.target}
                    </p>
                  </div>
                  <Badge kind={h.healthy ? "success" : "warning"}>
                    {h.healthy ? "探测正常" : "探测异常"}
                  </Badge>
                </div>
              ))}
            </div>
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
            <ActionButton id="fleet.add" variant="ghost" size="sm">
              添加服务器
            </ActionButton>
          }
        >
          {s.fleet?.length ? (
            <div className="px-6">
              {s.fleet.map((f) => (
                <div className="row-item" key={f.name}>
                  <div>
                    <p className="text-sm font-medium">{f.name}</p>
                    <p className="text-xs text-muted-foreground mt-1">
                      {f.host} · {dateTime(f.checked)}
                    </p>
                  </div>
                  <div className="flex items-center gap-2">
                    <Badge kind={f.online ? "success" : "default"}>
                      {f.online ? "在线" : "未知或离线"}
                    </Badge>
                    <ActionButton
                      id="fleet.remove"
                      context={{ name: f.name }}
                      variant="ghost"
                      size="sm"
                    >
                      移除
                    </ActionButton>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <Empty
              title="未添加监控服务器"
              description="登记后可在这里查看检查结果。"
              icon="server"
            />
          )}
        </Panel>
      </div>
    </>
  );
}
