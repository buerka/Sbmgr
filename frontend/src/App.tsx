import { useEffect, useState, type FormEvent } from "react";
import {
  Alert,
  Box,
  Breadcrumbs,
  Button,
  CircularProgress,
  Divider,
  Drawer,
  IconButton,
  LinearProgress,
  List,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  Paper,
  Snackbar,
  Stack,
  TextField,
  Tooltip,
  Typography,
  useMediaQuery,
  useTheme,
} from "@mui/material";
import { Link, Navigate, Route, Routes, useLocation } from "react-router-dom";
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
import { Overview } from "./pages/Overview";
import { Users } from "./pages/Users";
import { UserDetail } from "./pages/UserDetail";
import { RoutesPage } from "./pages/Routes";
import { Subscriptions } from "./pages/Subscriptions";
import { Operations } from "./pages/Operations";

const navigation: [string, string, IconName][] = [
  ["/overview", "总览", "home"],
  ["/users", "用户与设备", "users"],
  ["/routes", "线路与服务器", "routes"],
  ["/subscriptions", "订阅交付", "link"],
  ["/ops", "运维与备份", "settings"],
];
function Brand() {
  return (
    <Stack
      component={Link}
      to="/overview"
      direction="row"
      gap={1.3}
      alignItems="center"
      className="brand"
    >
      <Box className="brand-icon">
        <Icon name="server" sx={{ fontSize: 24 }} />
      </Box>
      <Typography component="span">sbmgr</Typography>
    </Stack>
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
    } catch (error) {
      setError(error instanceof Error ? error.message : "登录失败");
    } finally {
      const input = form.elements.namedItem(
        "password",
      ) as HTMLInputElement | null;
      if (input) input.value = "";
      setBusy(false);
    }
  }
  return (
    <Box className="login-shell">
      <Paper variant="outlined" className="login-panel">
        <Brand />
        <Typography variant="h1" sx={{ fontSize: 21, mt: 3, mb: 1 }}>
          登录管理控制台
        </Typography>
        <Typography variant="body2" color="text.secondary" mb={3}>
          管理你的用户、设备与线路。
        </Typography>
        <Box component="form" onSubmit={submit}>
          <Stack gap={2.5}>
            <TextField
              name="username"
              label="管理员账号"
              autoComplete="username"
              required
              autoFocus
              inputProps={{ maxLength: 64 }}
              disabled={busy}
            />
            <TextField
              name="password"
              label="密码"
              type="password"
              autoComplete="current-password"
              required
              inputProps={{ maxLength: 1024 }}
              disabled={busy}
            />
            {error && <Alert severity="error">{error}</Alert>}
            <Button variant="contained" type="submit" disabled={busy}>
              {busy ? "正在登录…" : "登录"}
            </Button>
          </Stack>
        </Box>
        <Divider sx={{ my: 3 }} />
        <Typography variant="caption" color="text.secondary">
          使用部署时设置的管理员账号。忘记密码时，请在服务器通过{" "}
          <code>sbmgr web configure</code> 重新设置。
        </Typography>
      </Paper>
      <Typography variant="caption" color="text.secondary" mt={2.5}>
        sbmgr 管理控制台
      </Typography>
    </Box>
  );
}
export function App() {
  const { status, snapshot, catalog, loading, error, dialog, job, notice } =
      useAppSelector((s) => s.admin),
    dispatch = useAppDispatch();
  const location = useLocation(),
    theme = useTheme(),
    mobile = useMediaQuery(theme.breakpoints.down("md"));
  const [drawer, setDrawer] = useState(false);
  const current = navigation.find(([path]) =>
    location.pathname.startsWith(path),
  );
  const title = location.pathname.startsWith("/users/")
    ? "用户详情"
    : current?.[1] || "总览";
  useEffect(() => {
    setDrawer(false);
    window.scrollTo(0, 0);
  }, [location.pathname]);
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
  }, [job?.id, job?.status, status, dispatch]);
  async function logout() {
    try {
      await api.logout();
    } finally {
      dispatch(signedOut());
    }
  }
  if (status === "checking")
    return (
      <Box className="loading-shell">
        <CircularProgress size={28} />
        <Typography color="text.secondary">正在连接管理服务…</Typography>
      </Box>
    );
  if (status === "anonymous") return <Login />;
  const sidebar = (
    <Box className="sidebar-inner">
      <Brand />
      <ActionButton
        id="user.add"
        variant="contained"
        className="sidebar-create"
      />
      <Typography
        className="nav-label"
        variant="caption"
        color="text.secondary"
      >
        工作空间
      </Typography>
      <List component="nav" aria-label="主要导航" disablePadding>
        {navigation.map(([path, label, icon], index) => (
          <Box key={path}>
            {index === 4 && <Divider sx={{ mx: 1.5, my: 2 }} />}
            <ListItemButton
              component={Link}
              to={path}
              selected={current?.[0] === path}
              aria-current={current?.[0] === path ? "page" : undefined}
            >
              <ListItemIcon sx={{ minWidth: 33 }}>
                <Icon name={icon} />
              </ListItemIcon>
              <ListItemText
                primary={label}
                primaryTypographyProps={{ fontSize: 13 }}
              />
              {path === "/users" && (
                <Typography variant="caption" color="text.secondary">
                  {snapshot?.users.length}
                </Typography>
              )}
            </ListItemButton>
          </Box>
        ))}
      </List>
      <Box className="sidebar-bottom">
        <Paper variant="outlined" className="connection-card">
          <Icon
            name={error ? "warning" : "check"}
            color={error ? "warning" : "primary"}
          />
          <Box>
            <Typography variant="body2">
              {error
                ? "状态读取失败"
                : snapshot?.role === "slave"
                  ? "从机已连接"
                  : "主控已连接"}
            </Typography>
            <Typography variant="caption" color="text.secondary">
              管理服务
            </Typography>
          </Box>
        </Paper>
        <Typography
          className="version"
          variant="caption"
          color="text.secondary"
        >
          sbmgr {snapshot?.version || ""}
        </Typography>
      </Box>
    </Box>
  );
  return (
    <Box className="app-shell">
      <Drawer
        variant={mobile ? "temporary" : "permanent"}
        open={mobile ? drawer : true}
        onClose={() => setDrawer(false)}
        PaperProps={{ component: "aside", className: "sidebar-paper" }}
      >
        {sidebar}
      </Drawer>
      <Box className="workspace">
        <Stack
          component="header"
          className="topbar"
          direction="row"
          alignItems="center"
          gap={1.5}
        >
          {mobile && (
            <IconButton
              aria-label="展开导航"
              aria-expanded={drawer}
              onClick={() => setDrawer(true)}
            >
              <Icon name="menu" />
            </IconButton>
          )}
          <Paper variant="outlined" className="path-bar">
            <Breadcrumbs separator={<Icon name="next" sx={{ fontSize: 15 }} />}>
              <Stack
                direction="row"
                alignItems="center"
                gap={1}
                className="path-root"
              >
                <Icon name="home" color="action" />
                <Typography variant="body2" color="text.secondary">
                  工作空间
                </Typography>
              </Stack>
              <Typography variant="body2" noWrap>
                {title}
              </Typography>
            </Breadcrumbs>
          </Paper>
          <Stack direction="row" alignItems="center" gap={0.7} flexShrink={0}>
            {(snapshot?.pending || snapshot?.mesh_pending) && (
              <Badge kind="warning">待应用</Badge>
            )}
            <Tooltip title="刷新状态">
              <span>
                <IconButton
                  aria-label="刷新状态"
                  disabled={loading}
                  onClick={() => {
                    void dispatch(refreshSnapshot());
                    if (!catalog.length) void dispatch(loadCatalog());
                  }}
                >
                  <Icon name="refresh" />
                </IconButton>
              </span>
            </Tooltip>
            <ActionButton id="config.apply" variant="contained" />
            <Divider
              orientation="vertical"
              flexItem
              className="top-divider"
              sx={{ mx: 0.5, my: 1 }}
            />
            <Tooltip title="退出登录">
              <IconButton aria-label="退出登录" onClick={() => void logout()}>
                <Icon name="logout" />
              </IconButton>
            </Tooltip>
          </Stack>
        </Stack>
        <Paper component="main" variant="outlined" className="main-panel">
          {error && (
            <Alert severity="error" sx={{ mb: 3 }}>
              {error}
            </Alert>
          )}
          {job && (
            <Alert
              severity={
                job.status === "failed"
                  ? "error"
                  : job.status === "success"
                    ? "success"
                    : "info"
              }
              sx={{ mb: 3 }}
            >
              <strong>{job.title}</strong> · {job.message || "正在执行…"}
              {job.status === "running" && <LinearProgress sx={{ mt: 1 }} />}
            </Alert>
          )}
          {snapshot?.role === "slave" && (
            <Alert severity="info" sx={{ mb: 3 }}>
              当前是从机。用户、设备与节点授权请在主机管理。
            </Alert>
          )}
          {!snapshot ? (
            <Stack alignItems="center" py={10} gap={2}>
              <CircularProgress size={25} />
              <Typography color="text.secondary">正在读取管理状态…</Typography>
            </Stack>
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
        </Paper>
        <Stack
          component="footer"
          direction="row"
          justifyContent="space-between"
          className="footer"
        >
          <Typography variant="caption">sbmgr 控制台</Typography>
          <Typography variant="caption">
            {snapshot
              ? `更新于 ${new Date(snapshot.time).toLocaleTimeString("zh-CN")}`
              : ""}
          </Typography>
        </Stack>
      </Box>
      <ActionDialog />
      <Snackbar
        open={Boolean(notice)}
        autoHideDuration={6500}
        onClose={(_, reason) => {
          if (reason !== "clickaway") dispatch(clearNotice());
        }}
        anchorOrigin={{ vertical: "bottom", horizontal: "center" }}
      >
        {notice ? (
          <Alert
            severity={notice.severity}
            variant="filled"
            onClose={() => dispatch(clearNotice())}
          >
            {notice.message}
          </Alert>
        ) : undefined}
      </Snackbar>
    </Box>
  );
}
