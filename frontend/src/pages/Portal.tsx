import { useEffect, useState, type ReactNode } from "react";
import {
  Link,
  Navigate,
  NavLink,
  Route,
  Routes,
  useLocation,
} from "react-router-dom";
import { api } from "../api";
import { signedOut, useAppDispatch, useAppSelector } from "../store";
import type { PortalSnapshot } from "../types";
import {
  Badge,
  DataTable,
  Empty,
  PageHeader,
  Panel,
} from "../components/common";
import { Icon } from "../components/Icons";
import { Button } from "../components/ui/button";
import { Alert, Progress } from "../components/ui/feedback";
import { TableCell, TableRow } from "../components/ui/table";
import { DeviceDelivery } from "../components/DeviceDelivery";
import { bytes, dateTime, rate } from "../format";
import { optionLabels } from "../components/formModel";
import { Account } from "./Account";
import { MyDevices } from "./MyDevices";
import { AnalyticsDashboard } from "../components/AnalyticsDashboard";
import "./user-workspace.css";

function MyStatus({ data }: { data: PortalSnapshot }) {
  const u = data.user,
    total = u.quota + u.extra_quota;
  return (
    <>
      <PageHeader
        title="我的代理状态"
        description="查看当前用量、配额和已分配线路。配额与权限如需调整，请联系管理员。"
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button asChild>
              <Link to="/me/analytics">
                <Icon name="health" /> 查看网站流量
              </Link>
            </Button>
            <Badge kind={u.status === "已启用" ? "success" : "warning"}>
              {u.status}
            </Badge>
          </div>
        }
      />
      <div className="user-summary">
        <div>
          <p>本期已用</p>
          <strong>
            {bytes(u.used)} <small>/ {u.quota ? bytes(total) : "不限"}</small>
          </strong>
          <Progress
            label="本期流量使用率"
            value={u.quota ? (u.used / total) * 100 : 0}
          />
        </div>
        <div>
          <p>剩余流量</p>
          <strong>
            {u.quota ? bytes(Math.max(0, total - u.used)) : "不限"}
          </strong>
          <small>{optionLabels[u.quota_mode] || "双向合计"}</small>
        </div>
        <div>
          <p>实时下载</p>
          <strong>{rate(u.current_down)}</strong>
          <small>上传 {rate(u.current_up)}</small>
        </div>
        <div>
          <p>有效期</p>
          <strong>{u.expires || "长期有效"}</strong>
          <small>
            {u.billing?.enabled && u.billing.next_reset
              ? `下次重置 ${dateTime(u.billing.next_reset)}`
              : "账期由管理员管理"}
          </small>
        </div>
      </div>
      <Panel
        title="已分配线路"
        description="显示当前授权状态与最近一次流量采样；没有流量不代表离线，授权可用也不代表线路已通过连通性测试。"
      >
        {u.nodes.length ? (
          <DataTable headings={["线路", "设备", "授权状态", "实时下载 / 上传"]}>
            {u.nodes.map((n, index) => (
              <TableRow key={`${n.device}/${n.name}/${index}`}>
                <TableCell className="font-medium">{n.name}</TableCell>
                <TableCell>
                  {u.devices.find((d) => d.name === n.device)?.label ||
                    n.device}
                </TableCell>
                <TableCell>
                  <Badge kind={n.available ? "success" : "warning"}>
                    {n.available ? "可使用" : "暂不可用"}
                  </Badge>
                </TableCell>
                <TableCell>
                  {rate(n.current_down)} / {rate(n.current_up)}
                </TableCell>
              </TableRow>
            ))}
          </DataTable>
        ) : (
          <Empty
            title="尚未分配线路"
            description="请联系管理员分配设备与线路。"
          />
        )}
      </Panel>
      <div className="mt-5 text-sm text-muted-foreground flex flex-wrap gap-x-6 gap-y-2">
        <span>累计上传 {bytes(u.upload)}</span>
        <span>累计下载 {bytes(u.download)}</span>
        <span>下载上限 {u.down_mbps ? `${u.down_mbps} Mbps` : "不限"}</span>
        <span>上传上限 {u.up_mbps ? `${u.up_mbps} Mbps` : "不限"}</span>
      </div>
    </>
  );
}

export function Portal({ themeControl }: { themeControl: ReactNode }) {
  const location = useLocation();
  const session = useAppSelector((s) => s.admin.session),
    notice = useAppSelector((s) => s.admin.notice),
    dispatch = useAppDispatch();
  const [data, setData] = useState<PortalSnapshot | null>(null),
    [error, setError] = useState(""),
    [loading, setLoading] = useState(false);
  async function refresh() {
    setLoading(true);
    try {
      setData(await api.me());
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "读取状态失败");
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    let active = true;
    const load = async () => {
      try {
        const next = await api.me();
        if (active) {
          setData(next);
          setError("");
        }
      } catch (e) {
        if (active) setError(e instanceof Error ? e.message : "读取状态失败");
      }
    };
    void load();
    const timer = setInterval(() => {
      if (!document.hidden) void load();
    }, 10000);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, []);
  return (
    <div className="min-h-screen bg-background">
      <header className="border-b">
        <div className="max-w-6xl mx-auto px-4 sm:px-8 py-4 flex items-center gap-3">
          <Link to="/me" className="flex items-center gap-2 font-semibold">
            <Icon name="routes" />
            sbmgr{" "}
            <span className="font-normal text-muted-foreground hidden sm:inline">
              我的服务
            </span>
          </Link>
          <span className="ml-auto text-sm truncate max-w-32">
            {session?.username}
          </span>
          <Button
            variant="ghost"
            size="icon"
            aria-label="刷新我的状态"
            disabled={loading}
            onClick={() => void refresh()}
          >
            <Icon name="refresh" className={loading ? "animate-spin" : ""} />
          </Button>
          {themeControl}
          <Button
            variant="ghost"
            size="icon"
            aria-label="退出登录"
            onClick={async () => {
              try {
                await api.logout();
              } finally {
                dispatch(signedOut());
              }
            }}
          >
            <Icon name="logout" />
          </Button>
        </div>
      </header>
      <main className="max-w-6xl mx-auto px-4 sm:px-8 py-6 sm:py-8">
        <nav
          aria-label="我的服务导航"
          className="flex gap-2 border-b pb-3 mb-7 overflow-x-auto"
        >
          {[
            ["/me", "我的状态"],
            ["/me/devices", "我的设备"],
            ["/me/analytics", "数据看板"],
            ["/me/subscriptions", "我的订阅"],
            ["/me/account", "密码设置"],
          ].map(([to, label]) => (
            <NavLink
              key={to}
              to={to}
              end
              className={({ isActive }) =>
                `px-3 py-2 rounded-md text-sm whitespace-nowrap ${isActive ? "bg-secondary font-medium" : "text-muted-foreground hover:bg-secondary"}`
              }
            >
              {label}
            </NavLink>
          ))}
        </nav>
        {error && (
          <Alert kind="error">
            {error} {data && "以下为上次成功读取的状态。"}
          </Alert>
        )}
        {notice && <Alert kind={notice.severity}>{notice.message}</Alert>}
        <Routes>
          <Route path="/me/account" element={<Account />} />
          <Route
            path="/me/analytics"
            element={<AnalyticsDashboard devices={data?.user.devices || []} />}
          />
          <Route
            path="/me/devices"
            element={
              data ? (
                <MyDevices data={data} refresh={refresh} />
              ) : (
                <Empty title="正在读取设备…" />
              )
            }
          />
          <Route
            path="/me"
            element={
              data ? (
                <MyStatus data={data} />
              ) : (
                <Empty title="正在读取我的状态…" />
              )
            }
          />
          <Route
            path="/me/subscriptions"
            element={
              data ? (
                <>
                  <PageHeader
                    title="我的订阅"
                    description="只包含你已获授权的设备和线路。可复制订阅链接，或下载 TXT / YAML。"
                  />
                  <DeviceDelivery
                    user={data.user}
                    readOnly
                    enabled={data.subscription_enabled}
                    selfService={{ version: data.device_version, refresh }}
                  />
                </>
              ) : (
                <Empty title="正在读取订阅状态…" />
              )
            }
          />
          <Route path="*" element={<Navigate to="/me" replace />} />
        </Routes>
        <p className="text-xs text-muted-foreground mt-6">
          {location.pathname === "/me/analytics"
            ? "网站统计按需读取；点击看板中的「刷新」获取最新采集结果。"
            : data
              ? `最近更新 ${dateTime(data.time)} · 每 10 秒刷新`
              : "正在连接服务"}
        </p>
      </main>
    </div>
  );
}
