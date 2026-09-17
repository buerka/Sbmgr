import { useEffect, useState, type FormEvent } from "react";
import {
  Link,
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router-dom";
import { api } from "./api";
import {
  clearNotice,
  jobReceived,
  loadCatalog,
  notify,
  refreshSnapshot,
  signedIn,
  signedOut,
  useAppDispatch,
  useAppSelector,
} from "./store";
import { ActionDialog } from "./components/ActionDialog";
import { ActionButton, Badge } from "./components/common";
import { Icon, type IconName } from "./components/Icons";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Alert } from "./components/ui/feedback";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "./components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from "./components/ui/dropdown-menu";
import { useTheme } from "./theme";
import { cn } from "./lib/utils";
import { Overview } from "./pages/Overview";
import { Users } from "./pages/Users";
import { UserDetail } from "./pages/UserDetail";
import { RoutesPage } from "./pages/Routes";
import { Subscriptions } from "./pages/Subscriptions";
import { Operations } from "./pages/Operations";
const navigation: [string, string, IconName][] = [
  ["/overview", "运行总览", "home"],
  ["/users", "用户管理", "users"],
  ["/routes", "线路管理", "routes"],
  ["/subscriptions", "订阅交付", "link"],
  ["/ops", "系统运维", "settings"],
];
function Brand() {
  return (
    <Link to="/overview" className="brand">
      <span className="brand-icon">
        <Icon name="routes" size={19} />
      </span>
      <span>
        <strong>sbmgr</strong>
        <small>网络管理控制台</small>
      </span>
      <Icon name="expand" className="ml-auto text-muted-foreground" />
    </Link>
  );
}
function ThemeMenu() {
  const { theme, setTheme } = useTheme();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          size="icon"
          variant="ghost"
          aria-label="切换主题"
          title="切换主题"
        >
          <Icon
            name={
              theme === "light" ? "sun" : theme === "dark" ? "moon" : "monitor"
            }
          />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuLabel>外观</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={theme}
          onValueChange={(value) => setTheme(value as typeof theme)}
        >
          {(
            [
              ["light", "浅色", "sun"],
              ["dark", "深色", "moon"],
              ["system", "跟随系统", "monitor"],
            ] as const
          ).map(([value, label, icon]) => (
            <DropdownMenuRadioItem value={value} key={value}>
              <Icon name={icon} />
              {label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
function Login() {
  const dispatch = useAppDispatch(),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    const form = event.currentTarget,
      data = new FormData(form);
    try {
      const session = await api.login(
        String(data.get("username") || ""),
        String(data.get("password") || ""),
      );
      form.reset();
      dispatch(signedIn(session));
      await Promise.all([dispatch(loadCatalog()), dispatch(refreshSnapshot())]);
    } catch (e) {
      setError(e instanceof Error ? e.message : "登录失败");
    } finally {
      const input = form.elements.namedItem(
        "password",
      ) as HTMLInputElement | null;
      if (input) input.value = "";
      setBusy(false);
    }
  }
  return (
    <main className="login-shell">
      <div className="absolute right-6 top-6">
        <ThemeMenu />
      </div>
      <div className="login-panel">
        <Brand />
        <div className="mt-8 mb-6">
          <h1>登录控制台</h1>
          <p className="text-muted-foreground text-sm mt-2">
            管理你的用户、设备与线路。
          </p>
        </div>
        <form onSubmit={submit} className="space-y-5">
          <div className="space-y-2">
            <label htmlFor="username">管理员账号</label>
            <Input
              id="username"
              name="username"
              autoComplete="username"
              required
              autoFocus
              maxLength={64}
              disabled={busy}
            />
          </div>
          <div className="space-y-2">
            <label htmlFor="password">密码</label>
            <Input
              id="password"
              name="password"
              type="password"
              autoComplete="current-password"
              required
              maxLength={1024}
              disabled={busy}
            />
          </div>
          {error && <Alert kind="error">{error}</Alert>}
          <Button className="w-full" type="submit" disabled={busy}>
            {busy ? "正在登录…" : "登录"}
          </Button>
        </form>
        <p className="text-xs text-muted-foreground mt-6 leading-relaxed">
          使用部署时设置的管理员账号。忘记密码时，请在服务器通过{" "}
          <code>sbmgr web configure</code> 重新设置。
        </p>
      </div>
    </main>
  );
}
function NavigationSearch({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [query, setQuery] = useState("");
  const users = useAppSelector((s) => s.admin.snapshot?.users) || [],
    navigate = useNavigate();
  const entries = [
    ...navigation.map(([path, label, icon]) => ({
      path,
      label,
      icon,
      group: "页面",
    })),
    ...users.map((u) => ({
      path: `/users/${encodeURIComponent(u.name)}`,
      label: u.name,
      icon: "users" as IconName,
      group: "用户",
    })),
  ]
    .filter((e) => e.label.toLowerCase().includes(query.toLowerCase()))
    .slice(0, 12);
  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v);
        if (!v) setQuery("");
      }}
    >
      <DialogContent
        className="p-0 gap-0 overflow-hidden"
        showCloseButton={false}
      >
        <DialogTitle className="sr-only">搜索页面与用户</DialogTitle>
        <DialogDescription className="sr-only">
          输入名称快速跳转，按 Tab 选择结果，回车打开。
        </DialogDescription>
        <div className="flex items-center gap-3 px-4 border-b h-14">
          <Icon name="search" className="text-muted-foreground" />
          <input
            autoFocus
            aria-label="搜索页面与用户"
            placeholder="搜索页面、用户…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="flex-1 bg-transparent outline-none min-w-0 text-sm"
          />
          <kbd>Esc</kbd>
        </div>
        <div className="max-h-80 overflow-y-auto p-2">
          {entries.map((e) => (
            <button
              key={e.path}
              aria-label={`打开${e.group}：${e.label}`}
              className="command-result"
              onClick={() => {
                navigate(e.path);
                onOpenChange(false);
                setQuery("");
              }}
            >
              <Icon name={e.icon} />
              <span>{e.label}</span>
              <small>{e.group}</small>
            </button>
          ))}
          {!entries.length && (
            <p className="py-8 text-center text-sm text-muted-foreground">
              没有找到匹配的页面或用户
            </p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
export function App() {
  const {
      status,
      snapshot,
      catalog,
      loading,
      error,
      dialog,
      job,
      notice,
      session,
    } = useAppSelector((s) => s.admin),
    dispatch = useAppDispatch(),
    location = useLocation();
  const [collapsed, setCollapsed] = useState(false),
    [mobileOpen, setMobileOpen] = useState(false),
    [searchOpen, setSearchOpen] = useState(false);
  const current = navigation.find(([path]) =>
    location.pathname.startsWith(path),
  );
  useEffect(() => {
    setMobileOpen(false);
    window.scrollTo(0, 0);
  }, [location.pathname]);
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setSearchOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);
  useEffect(() => {
    if (!notice) return;
    const timer = setTimeout(() => dispatch(clearNotice()), 6500);
    return () => clearTimeout(timer);
  }, [notice, dispatch]);
  useEffect(() => {
    if (status !== "authenticated") return;
    const timer = setInterval(() => {
      if (!document.hidden && !dialog) void dispatch(refreshSnapshot());
    }, 10000);
    return () => clearInterval(timer);
  }, [status, dialog, dispatch]);
  useEffect(() => {
    if (status !== "authenticated" || !job || job.status !== "running") return;
    let cancelled = false,
      timer: ReturnType<typeof setTimeout>,
      reported = false;
    const controller = new AbortController(),
      id = job.id;
    const poll = async () => {
      try {
        const result = await api.job(id, controller.signal);
        if (cancelled) return;
        dispatch(jobReceived(result));
        if (result.status !== "running") {
          if (result.status !== "failed" || !dialog)
            dispatch(
              notify({
                message: result.message,
                severity: result.status === "failed" ? "error" : "success",
              }),
            );
          void dispatch(refreshSnapshot());
          return;
        }
        reported = false;
      } catch {
        if (cancelled) return;
        if (!reported) {
          dispatch(
            notify({
              message: "暂时无法确认任务结果，正在重新连接。",
              severity: "error",
            }),
          );
          reported = true;
        }
      }
      if (!cancelled)
        timer = setTimeout(() => void poll(), reported ? 2500 : 700);
    };
    timer = setTimeout(() => void poll(), 700);
    return () => {
      cancelled = true;
      clearTimeout(timer);
      controller.abort();
    };
  }, [job?.id, job?.status, status, dispatch, dialog]);
  async function logout() {
    try {
      await api.logout();
    } finally {
      dispatch(signedOut());
    }
  }

  if (status === "checking")
    return (
      <div className="loading-shell">
        <Icon name="loading" size={24} className="animate-spin" />
        <p>正在连接管理服务…</p>
      </div>
    );
  if (status === "anonymous") return <Login />;
  const sidebar = (
    <>
      <Brand />
      <nav aria-label="主要导航">
        <p className="nav-group-label">工作空间</p>
        {navigation.map(([path, label, icon], index) => (
          <div key={path}>
            {index === 4 && <p className="nav-group-label mt-7">系统</p>}
            <Link
              title={collapsed ? label : undefined}
              to={path}
              onClick={() => setMobileOpen(false)}
              aria-current={current?.[0] === path ? "page" : undefined}
              className={cn("nav-item", current?.[0] === path && "active")}
            >
              <Icon name={icon} />
              <span>{label}</span>
              {path === "/users" && (
                <small>{snapshot?.users.length ?? 0}</small>
              )}
            </Link>
          </div>
        ))}
      </nav>
      <div className="sidebar-bottom">
        <div className="sidebar-state">
          <span className={cn("state-dot", error && "state-error")} />
          <span>
            {error
              ? "状态读取失败"
              : snapshot?.role === "slave"
                ? "从机已连接"
                : "管理服务已连接"}
          </span>
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button className="account-button" aria-label="管理员菜单">
              <span className="avatar">
                {(session?.username || "AD").slice(0, 2).toUpperCase()}
              </span>
              <span className="account-copy">
                <strong>{session?.username || "管理员"}</strong>
                <small>
                  {snapshot?.role === "slave" ? "从机" : "管理工作空间"} ·{" "}
                  {snapshot?.version}
                </small>
              </span>
              <Icon name="expand" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent side="top" align="start" className="w-52">
            <DropdownMenuLabel>管理会话</DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => void logout()}>
              <Icon name="logout" />
              退出登录
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </>
  );
  return (
    <div className={cn("app-shell", collapsed && "sidebar-collapsed")}>
      <a
        href="#content"
        onClick={(event) => {
          event.preventDefault();
          document.getElementById("content")?.focus();
        }}
        className="skip-link"
      >
        跳到主要内容
      </a>
      <aside className="desktop-sidebar">{sidebar}</aside>
      <Dialog open={mobileOpen} onOpenChange={setMobileOpen}>
        <DialogContent
          className="mobile-sidebar"
          placement="left"
          showCloseButton={false}
        >
          <DialogTitle className="sr-only">导航</DialogTitle>
          <DialogDescription className="sr-only">
            工作空间导航与管理员操作
          </DialogDescription>
          {sidebar}
        </DialogContent>
      </Dialog>
      <div className="workspace">
        <header className="topbar">
          <Button
            variant="ghost"
            size="icon"
            className="desktop-toggle"
            aria-label={collapsed ? "展开导航" : "折叠导航"}
            aria-expanded={!collapsed}
            onClick={() => setCollapsed((v) => !v)}
          >
            <Icon name="menu" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="mobile-toggle"
            aria-label="展开导航"
            aria-expanded={mobileOpen}
            onClick={() => setMobileOpen(true)}
          >
            <Icon name="menu" />
          </Button>
          <span className="topbar-divider" />
          <Button
            variant="outline"
            className="search-trigger"
            aria-label="搜索页面与用户"
            onClick={() => setSearchOpen(true)}
          >
            <Icon name="search" />
            <span>搜索页面、用户…</span>
            <kbd>Ctrl K</kbd>
          </Button>
          <div className="ml-auto flex items-center gap-1 sm:gap-2">
            {(snapshot?.pending || snapshot?.mesh_pending) && (
              <span className="pending-indicator">
                <Badge kind="warning">待应用</Badge>
              </span>
            )}
            <Button
              variant="ghost"
              size="icon"
              aria-label="刷新状态"
              title="刷新状态"
              disabled={loading}
              onClick={() => {
                void dispatch(refreshSnapshot());
                if (!catalog.length) void dispatch(loadCatalog());
              }}
            >
              <Icon name="refresh" className={loading ? "animate-spin" : ""} />
            </Button>
            <ThemeMenu />
            {snapshot?.pending && (
              <ActionButton id="config.apply" size="sm" variant="default" />
            )}
            <Button
              variant="ghost"
              size="icon"
              aria-label="退出登录"
              title="退出登录"
              onClick={() => void logout()}
            >
              <Icon name="logout" />
            </Button>
          </div>
        </header>
        <main id="content" tabIndex={-1} className="main-content">
          {error && <Alert kind="error">{error}</Alert>}
          {job && job.status !== "success" && (
            <Alert kind={job.status === "failed" ? "error" : "info"}>
              <strong>{job.title}</strong> · {job.message || "正在执行…"}
            </Alert>
          )}
          {snapshot?.role === "slave" && (
            <Alert>当前是从机。用户、设备与节点授权请在主机管理。</Alert>
          )}
          {!snapshot ? (
            <div className="loading-shell">
              <Icon name="loading" className="animate-spin" />
              正在读取管理状态…
            </div>
          ) : (
            <Routes>
              <Route path="/overview" element={<Overview />} />
              <Route path="/users" element={<Users />} />
              <Route path="/users/:name" element={<UserDetail />} />
              <Route path="/routes" element={<RoutesPage />} />
              <Route path="/subscriptions" element={<Subscriptions />} />
              <Route path="/ops" element={<Operations />} />
              <Route path="*" element={<Navigate to="/overview" replace />} />
            </Routes>
          )}
        </main>
        <footer className="app-footer">
          <span>sbmgr 控制台</span>
          <span>
            {snapshot
              ? `更新于 ${new Date(snapshot.time).toLocaleTimeString("zh-CN")}`
              : ""}
          </span>
        </footer>
      </div>
      <NavigationSearch open={searchOpen} onOpenChange={setSearchOpen} />
      <ActionDialog />
      {notice && (
        <div className="toast">
          <Alert kind={notice.severity}>
            <div className="flex items-center gap-4">
              <span>{notice.message}</span>
              <Button
                variant="ghost"
                size="icon"
                aria-label="关闭通知"
                onClick={() => dispatch(clearNotice())}
              >
                <Icon name="close" />
              </Button>
            </div>
          </Alert>
        </div>
      )}
    </div>
  );
}
