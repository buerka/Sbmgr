import { useState } from "react";
import { api } from "../api";
import { notify, signedOut, useAppDispatch, useAppSelector } from "../store";
import { PageHeader } from "../components/common";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Alert } from "../components/ui/feedback";
import { Icon } from "../components/Icons";

export function Account() {
  const session = useAppSelector((s) => s.admin.session);
  const job = useAppSelector((s) => s.admin.job);
  const dispatch = useAppDispatch();
  const [username, setUsername] = useState(session?.username || "");
  const [current, setCurrent] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const changed = username.trim() !== session?.username || !!password;
  return (
    <>
      <PageHeader
        title="管理账号"
        description="修改当前面板的登录用户名与密码。"
      />
      <div className="account-settings">
        <aside>
          <span className="surface-icon">
            <Icon name="shield" size={22} />
          </span>
          <h2>登录信息</h2>
          <p>
            当前账号 <strong>{session?.username}</strong>
          </p>
          <p>修改成功后，所有已登录会话都会退出。下次使用新的账号信息登录。</p>
        </aside>
        <form
          className="account-form"
          onSubmit={async (e) => {
            e.preventDefault();
            setError("");
            if (password !== confirm) {
              setError("两次输入的新密码不一致。");
              return;
            }
            if (password && new TextEncoder().encode(password).length < 12) {
              setError(
                "新密码至少需要 12 字节，建议使用 12 位以上的字母、数字与符号。",
              );
              return;
            }
            setBusy(true);
            try {
              const result = await api.account(
                username.trim(),
                current,
                password,
              );
              setCurrent("");
              setPassword("");
              setConfirm("");
              dispatch(signedOut());
              dispatch(
                notify({ message: result.message, severity: "success" }),
              );
            } catch (e) {
              setError(e instanceof Error ? e.message : "修改失败，请重试。");
            } finally {
              setBusy(false);
            }
          }}
        >
          <fieldset disabled={busy} className="space-y-6">
            <div>
              <label htmlFor="admin-username">用户名</label>
              <Input
                id="admin-username"
                autoComplete="username"
                value={username}
                maxLength={64}
                required
                onChange={(e) => setUsername(e.target.value)}
              />
              <p className="field-hint">当前值已填入；仅修改密码时保留此项。</p>
            </div>
            <div>
              <label htmlFor="admin-current">当前密码</label>
              <Input
                id="admin-current"
                autoComplete="current-password"
                type="password"
                value={current}
                required
                maxLength={1024}
                onChange={(e) => setCurrent(e.target.value)}
              />
              <p className="field-hint">
                修改用户名或密码，都需要验证当前密码。
              </p>
            </div>
            <div className="account-passwords">
              <div>
                <label htmlFor="admin-new">新密码</label>
                <Input
                  id="admin-new"
                  autoComplete="new-password"
                  type="password"
                  value={password}
                  maxLength={1024}
                  onChange={(e) => setPassword(e.target.value)}
                />
                <p className="field-hint">留空保留原密码，至少 12 字节。</p>
              </div>
              <div>
                <label htmlFor="admin-confirm">确认新密码</label>
                <Input
                  id="admin-confirm"
                  autoComplete="new-password"
                  type="password"
                  value={confirm}
                  maxLength={1024}
                  required={!!password}
                  onChange={(e) => setConfirm(e.target.value)}
                />
              </div>
            </div>
          </fieldset>
          {error && <Alert kind="error">{error}</Alert>}
          <div className="account-footer">
            <p>仅更新本机的管理账号，即时生效。</p>
            <Button
              type="submit"
              disabled={
                busy ||
                !changed ||
                !current ||
                !username.trim() ||
                job?.status === "running"
              }
            >
              {busy ? "正在保存…" : "保存并重新登录"}
            </Button>
          </div>
        </form>
      </div>
    </>
  );
}
