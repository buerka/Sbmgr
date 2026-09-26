import { useEffect, useLayoutEffect, useState } from "react";
import { api } from "../api";
import type { User } from "../types";
import { dateTime } from "../format";
import { notify, refreshSnapshot, useAppDispatch } from "../store";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Switch } from "./ui/switch";
import { Alert } from "./ui/feedback";
import { Badge } from "./common";
import { Icon } from "./Icons";

export function PortalAccess({
  user,
  disabled,
  onDirty,
}: {
  user: User;
  disabled: boolean;
  onDirty: (id: string, dirty: boolean) => void;
}) {
  const current = Boolean(user.portal?.enabled),
    configured = Boolean(user.portal?.configured),
    invited = Boolean(user.portal?.invited);
  const inviteExpired =
    invited && Date.parse(user.portal?.invite_expires || "") <= Date.now();
  const [enabled, setEnabled] = useState(current),
    [baseline, setBaseline] = useState(current),
    [password, setPassword] = useState(""),
    [confirm, setConfirm] = useState(""),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [invitation, setInvitation] = useState<{
      url: string;
      expires: string;
    } | null>(null),
    [copied, setCopied] = useState(false);
  const dispatch = useAppDispatch(),
    dirty = enabled !== baseline || !!password || !!confirm;
  useEffect(() => {
    if (!dirty && !busy) {
      setEnabled(current);
      setBaseline(current);
    }
  }, [current, dirty, busy]);
  useEffect(() => {
    if (configured || !invited) setInvitation(null);
  }, [configured, invited]);
  useLayoutEffect(
    () => onDirty("portal.account", dirty || busy),
    [dirty, busy, onDirty],
  );
  async function apply(operation: () => Promise<{ message: string }>) {
    setBusy(true);
    setError("");
    try {
      const result = await operation();
      setPassword("");
      setConfirm("");
      await dispatch(refreshSnapshot()).unwrap();
      dispatch(notify({ message: result.message, severity: "success" }));
    } catch (e) {
      setError(e instanceof Error ? e.message : "操作失败，请刷新状态后重试。");
    } finally {
      setBusy(false);
      setPassword("");
      setConfirm("");
    }
  }
  return (
    <section className="rounded-xl border p-5 sm:p-6 space-y-6">
      <div className="flex flex-wrap justify-between gap-4">
        <div>
          <h2 className="font-semibold">面板登录</h2>
          <p className="text-sm text-muted-foreground mt-2">
            登录名 <strong>{user.name}</strong>
            。仅能查看自己的用量、状态与订阅，并修改自己的密码。
          </p>
        </div>
        <Badge
          kind={
            configured && current ? "success" : invited ? "warning" : "default"
          }
        >
          {configured
            ? current
              ? "已开通"
              : "已停用"
            : invited
              ? inviteExpired
                ? "邀请已过期"
                : "等待设置密码"
              : "未开通"}
        </Badge>
      </div>
      {!configured ? (
        <div className="space-y-5">
          <div className="rounded-lg bg-muted/40 border p-4 sm:p-5 flex gap-4">
            <Icon name="link" className="text-muted-foreground shrink-0 mt-1" />
            <div className="space-y-2">
              <h3 className="font-medium">由用户设置自己的密码</h3>
              <p className="text-sm text-muted-foreground leading-relaxed">
                新用户没有默认密码。生成邀请链接并发送给该用户，用户设置密码成功后即可登录，链接随即作废。
              </p>
              <p className="text-sm text-muted-foreground">
                邀请有效期 24 小时；重新生成会立即作废旧链接。
              </p>
              {invited && user.portal?.invite_expires && (
                <p className="text-sm">
                  当前邀请有效期至 {dateTime(user.portal.invite_expires)}
                </p>
              )}
            </div>
          </div>
          <div className="flex flex-wrap gap-3">
            <Button
              disabled={disabled || busy}
              onClick={() =>
                void apply(async () => {
                  const result = await api.portalInvite(user.name);
                  setInvitation(result);
                  setCopied(false);
                  setEnabled(true);
                  setBaseline(true);
                  return result;
                })
              }
            >
              <Icon name="link" />
              {busy
                ? "正在处理…"
                : invited
                  ? "重新生成邀请链接"
                  : "生成邀请链接"}
            </Button>
            {invited && (
              <Button
                variant="outline"
                disabled={disabled || busy}
                onClick={() =>
                  void apply(async () => {
                    const result = await api.portalAccount(
                      user.name,
                      false,
                      "",
                    );
                    setInvitation(null);
                    setEnabled(false);
                    setBaseline(false);
                    return result;
                  })
                }
              >
                撤销邀请
              </Button>
            )}
          </div>
          {invitation && (
            <div className="rounded-lg border p-4 space-y-3" role="status">
              <label htmlFor="portal-invitation" className="font-medium">
                邀请链接
              </label>
              <div className="flex flex-col sm:flex-row gap-2">
                <Input
                  id="portal-invitation"
                  readOnly
                  value={invitation.url}
                  onFocus={(e) => e.target.select()}
                  className="min-w-0 font-mono text-xs"
                />
                <Button
                  variant="outline"
                  disabled={busy}
                  onClick={async () => {
                    try {
                      await navigator.clipboard.writeText(invitation.url);
                      setCopied(true);
                    } catch {
                      setError("无法使用剪贴板，请选中邀请链接手动复制。");
                    }
                  }}
                >
                  <Icon name="copy" />
                  {copied ? "已复制" : "复制链接"}
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">
                请仅发送给 {user.name}
                。链接只在本次生成后显示，关闭页面后如需获取请重新生成。
              </p>
            </div>
          )}
          <p className="text-sm text-muted-foreground">
            生成与撤销立即生效，无需应用代理配置。保存失败时原邀请保持不变。
          </p>
        </div>
      ) : (
        <form
          className="space-y-6"
          onSubmit={(e) => {
            e.preventDefault();
            setError("");
            if (password !== confirm) {
              setError("两次输入的密码不一致。");
              return;
            }
            if (password && new TextEncoder().encode(password).length < 12) {
              setError("密码至少 12 字节。");
              return;
            }
            void apply(async () => {
              const result = await api.portalAccount(
                user.name,
                enabled,
                password,
              );
              setBaseline(enabled);
              return result;
            });
          }}
        >
          <fieldset disabled={disabled || busy} className="space-y-5">
            <div className="flex items-center gap-3">
              <Switch
                id="portal-enabled"
                checked={enabled}
                onCheckedChange={setEnabled}
              />
              <label htmlFor="portal-enabled">允许该用户登录面板</label>
            </div>
            <div className="grid sm:grid-cols-2 gap-5">
              <div>
                <label htmlFor="portal-password">重置登录密码</label>
                <Input
                  id="portal-password"
                  type="password"
                  autoComplete="new-password"
                  value={password}
                  maxLength={1024}
                  onChange={(e) => setPassword(e.target.value)}
                />
                <p className="field-hint">
                  无需原密码；留空保持原密码。至少 12 字节。
                </p>
              </div>
              <div>
                <label htmlFor="portal-confirm">确认新密码</label>
                <Input
                  id="portal-confirm"
                  type="password"
                  autoComplete="new-password"
                  value={confirm}
                  maxLength={1024}
                  required={!!password}
                  onChange={(e) => setConfirm(e.target.value)}
                />
              </div>
            </div>
          </fieldset>
          <p className="text-sm text-muted-foreground">
            保存立即生效。停用或重置密码会使该用户的旧登录会话失效；代理身份和订阅链接不变。保存失败保留原配置，无需应用代理配置。
          </p>
          <div className="flex justify-end gap-2">
            <Button
              type="button"
              variant="outline"
              disabled={busy || !dirty}
              onClick={() => {
                setEnabled(current);
                setBaseline(current);
                setPassword("");
                setConfirm("");
                setError("");
              }}
            >
              取消修改
            </Button>
            <Button type="submit" disabled={disabled || busy || !dirty}>
              {busy
                ? "正在保存…"
                : password
                  ? "保存并重置密码"
                  : "保存登录设置"}
            </Button>
          </div>
        </form>
      )}
      {disabled && <Alert>从机不提供账号开通，请在主机管理。</Alert>}
      {error && <Alert kind="error">{error}</Alert>}
    </section>
  );
}
