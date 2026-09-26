import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { api } from "../api";
import { dateTime } from "../format";
import { Icon } from "../components/Icons";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Alert } from "../components/ui/feedback";

export function Activate({ themeControl }: { themeControl: ReactNode }) {
  const location = useLocation(),
    navigate = useNavigate();
  const [invitation, setInvitation] = useState(() => ({
    token: new URLSearchParams(location.search).get("token") || "",
  }));
  const token = invitation.token;
  const [info, setInfo] = useState<{
      username: string;
      expires: string;
    } | null>(null),
    [password, setPassword] = useState(""),
    [confirm, setConfirm] = useState(""),
    [busy, setBusy] = useState(false),
    [loading, setLoading] = useState(true),
    [error, setError] = useState(""),
    [success, setSuccess] = useState("");
  useEffect(() => {
    // Keep invitations in component memory only; do not persist in storage or Redux.
    if (location.search) {
      setInvitation({
        token: new URLSearchParams(location.search).get("token") || "",
      });
      setInfo(null);
      setError("");
      setSuccess("");
      setPassword("");
      setConfirm("");
      setLoading(true);
      void navigate("/activate", { replace: true });
    }
  }, [location.search, navigate]);
  useEffect(() => {
    let active = true;
    if (!token) {
      setLoading(false);
      return;
    }
    api
      .inviteInfo(token)
      .then((result) => {
        if (active) setInfo(result);
      })
      .catch((e) => {
        if (active)
          setError(
            e instanceof Error ? e.message : "无法读取邀请，请重新打开链接。",
          );
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [invitation]);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    if (password !== confirm) {
      setError("两次输入的密码不一致。");
      return;
    }
    const length = new TextEncoder().encode(password).length;
    if (length < 12 || length > 1024) {
      setError("密码长度须为 12–1024 字节。");
      return;
    }
    setBusy(true);
    try {
      const result = await api.acceptInvite(token, password);
      setSuccess(result.message);
      setInvitation({ token: "" });
    } catch (e) {
      setError(e instanceof Error ? e.message : "设置失败，请稍后重试。");
    } finally {
      setPassword("");
      setConfirm("");
      setBusy(false);
    }
  }
  return (
    <main className="login-shell">
      <div className="absolute right-6 top-6">{themeControl}</div>
      <section className="login-panel">
        <div className="flex items-center gap-3 mb-8">
          <span className="brand-icon">
            <Icon name="shield" size={20} />
          </span>
          <span className="font-semibold text-lg">
            sbmgr{" "}
            <span className="font-normal text-muted-foreground">
              · 我的服务
            </span>
          </span>
        </div>
        <h1>{success ? "账号已开通" : "设置你的登录密码"}</h1>
        {success ? (
          <div className="space-y-6 mt-5">
            <Alert kind="success">{success}</Alert>
            <Button asChild className="w-full">
              <Link to="/me">前往登录</Link>
            </Button>
          </div>
        ) : loading ? (
          <p className="mt-5 text-muted-foreground" role="status">
            正在检查邀请…
          </p>
        ) : info ? (
          <form className="space-y-5 mt-5" onSubmit={submit}>
            <p className="text-sm text-muted-foreground">
              管理员邀请你查看自己的代理状态与订阅。设置成功后，这条邀请链接将自动作废。
            </p>
            <div>
              <label htmlFor="invite-username">登录账号</label>
              <Input
                id="invite-username"
                value={info.username}
                readOnly
                autoComplete="username"
                className="mt-2 bg-muted/30"
              />
            </div>
            <div>
              <label htmlFor="invite-password">创建密码</label>
              <Input
                id="invite-password"
                type="password"
                autoComplete="new-password"
                value={password}
                required
                maxLength={1024}
                disabled={busy}
                onChange={(e) => setPassword(e.target.value)}
                className="mt-2"
              />
              <p className="field-hint">
                至少 12 字节，建议使用较长的独立密码。
              </p>
            </div>
            <div>
              <label htmlFor="invite-confirm">确认密码</label>
              <Input
                id="invite-confirm"
                type="password"
                autoComplete="new-password"
                value={confirm}
                required
                maxLength={1024}
                disabled={busy}
                onChange={(e) => setConfirm(e.target.value)}
                className="mt-2"
              />
            </div>
            {error && <Alert kind="error">{error}</Alert>}
            <Button type="submit" disabled={busy} className="w-full">
              {busy ? "正在设置…" : "设置密码并开通"}
            </Button>
            <p className="text-xs text-muted-foreground">
              邀请有效期至 {dateTime(info.expires)}
              。密码保存失败时，可用原邀请重试。
            </p>
          </form>
        ) : (
          <div className="space-y-5 mt-5">
            <Alert kind="error">
              {error || "请重新打开完整的邀请链接，或联系管理员生成新邀请。"}
            </Alert>
            <Button variant="outline" asChild>
              <Link to="/me">返回登录</Link>
            </Button>
          </div>
        )}
      </section>
    </main>
  );
}
