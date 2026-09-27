import { useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import type { Device, PortalSnapshot } from "../types";
import { PageHeader, Badge } from "../components/common";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Alert } from "../components/ui/feedback";
import { Icon } from "../components/Icons";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "../components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../components/ui/select";
import { ResetSubscriptionLink } from "../components/ResetSubscriptionLink";
import { bytes } from "../format";

type Edit = {
  action: "add" | "rename" | "delete";
  device?: Device;
  name: string;
  from: string;
  expected: string;
};

export function MyDevices({
  data,
  refresh,
}: {
  data: PortalSnapshot;
  refresh: () => Promise<void>;
}) {
  const u = data.user,
    limit = u.device_limit || 0;
  const [edit, setEdit] = useState<Edit | null>(null),
    [dialogOpen, setDialogOpen] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState("");
  const sources = u.devices.filter(
    (d) => d.deliverable && u.nodes.some((n) => n.device === d.name),
  );
  const active = limit > 0 && u.enabled && u.status === "已启用";
  const canAdd =
    active && u.devices.length < limit && sources.length > 0 && !data.pending;
  function open(action: Edit["action"], device?: Device) {
    setDialogOpen(true);
    setError("");
    setEdit({
      action,
      device,
      name: device?.label || device?.name || "",
      from: sources[0]?.name || "",
      expected: data.device_version,
    });
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!edit || busy) return;
    setBusy(true);
    setError("");
    try {
      const reply = await api.selfDevice({
        action: edit.action,
        device: edit.device?.name,
        name: edit.action !== "delete" ? edit.name.trim() : undefined,
        from: edit.action === "add" ? edit.from : undefined,
        expected: edit.expected,
      });
      setNotice(reply.message);
      setDialogOpen(false);
      await refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : "操作失败，请刷新后重试。");
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <PageHeader
        title="我的设备"
        description="给每台设备一份独立订阅，所有设备共用你的流量配额。"
        actions={
          <Button disabled={!canAdd || busy} onClick={() => open("add")}>
            <Icon name="add" /> 添加设备
          </Button>
        }
      />
      <div className="rounded-xl border bg-card p-5 sm:p-6 mb-6 flex flex-wrap items-center justify-between gap-4">
        <div>
          <p className="text-sm text-muted-foreground">设备名额</p>
          <p className="text-3xl font-semibold tabular-nums mt-2">
            {u.devices.length}
            <span className="text-base font-normal text-muted-foreground">
              {" "}
              / {limit || "未开放"}
            </span>
          </p>
        </div>
        <div className="text-sm text-muted-foreground max-w-md space-y-1">
          <p>
            {limit
              ? `还可添加 ${Math.max(0, limit - u.devices.length)} 台，停用设备也占用名额。`
              : "管理员尚未开放自助管理，现有设备与订阅不受影响。"}
          </p>
          <p>名额不是同时在线数量，访问和并发规则仍然有效。</p>
        </div>
      </div>
      {notice && <Alert className="mb-5">{notice}</Alert>}
      {data.pending && (
        <Alert className="mb-5">
          本机配置仍待应用。新增、删除或重置订阅后的入口更新可能需要一些时间；远端入口还可能继续同步。若刚重置订阅，旧地址已立即失效，旧客户端配置须待各入口应用后失效。持续未完成请联系管理员。
        </Alert>
      )}
      {limit > 0 && u.devices.length >= limit && (
        <p className="text-sm text-muted-foreground mb-5">
          {u.devices.length > limit
            ? "现有设备超过新名额，仍会保留。"
            : "设备名额已用完。"}{" "}
          删除不再使用的设备可释放名额，也可以联系管理员调整。
        </p>
      )}
      <div className="grid gap-4 md:grid-cols-2">
        {u.devices.map((d) => {
          const nodes = u.nodes.filter((n) => n.device === d.name);
          return (
            <section
              key={d.name}
              className="rounded-xl border bg-card p-5 sm:p-6 space-y-5"
            >
              <div className="flex items-start gap-3">
                <span className="surface-icon">
                  <Icon name="device" />
                </span>
                <div className="min-w-0 flex-1">
                  <h2 className="font-semibold break-all">
                    {d.label || d.name}
                  </h2>
                  <p className="text-xs text-muted-foreground mt-1">
                    {nodes.length} 条已授权线路 · 累计用量{" "}
                    {bytes(d.upload + d.download)}
                  </p>
                </div>
                <Badge kind={d.deliverable ? "success" : "default"}>
                  {d.deliverable ? "可使用" : "暂不可用"}
                </Badge>
              </div>
              <div className="flex flex-wrap gap-2">
                {nodes.map((n) => (
                  <Badge key={n.name}>{n.name}</Badge>
                ))}
                {!nodes.length && (
                  <span className="text-sm text-muted-foreground">
                    尚无线路，请联系管理员。
                  </span>
                )}
              </div>
              <div className="flex flex-wrap gap-2 border-t pt-4">
                <Button variant="outline" asChild>
                  <Link
                    to={`/me/analytics?device=${encodeURIComponent(d.name)}`}
                  >
                    <Icon name="health" /> 查看网站流量
                    <Icon name="next" />
                  </Link>
                </Button>
                <Button variant="outline" asChild>
                  <Link to="/me/subscriptions">
                    获取订阅
                    <Icon name="next" />
                  </Link>
                </Button>
                <Button
                  variant="ghost"
                  disabled={!active || busy}
                  onClick={() => open("rename", d)}
                >
                  重命名
                </Button>
                <ResetSubscriptionLink
                  device={d}
                  version={data.device_version}
                  refresh={refresh}
                />
                <Button
                  variant="ghost"
                  className="text-destructive ml-auto"
                  disabled={
                    !active ||
                    busy ||
                    data.pending ||
                    !sources.some((source) => source.name !== d.name)
                  }
                  title={
                    !sources.some((source) => source.name !== d.name)
                      ? "至少保留一台可用且已有线路的设备"
                      : undefined
                  }
                  onClick={() => open("delete", d)}
                >
                  删除
                </Button>
              </div>
            </section>
          );
        })}
      </div>
      <p className="text-xs text-muted-foreground mt-5">
        至少保留一台可用且已有线路的设备作为配置来源。删除设备不会返还已经使用的流量；重命名不会改变订阅地址、节点身份或已有连接。
      </p>
      <Dialog
        open={dialogOpen}
        onOpenChange={(open) => {
          if (!open && !busy) {
            setDialogOpen(false);
            setError("");
          }
        }}
      >
        <DialogContent
          className="sm:max-w-lg"
          onEscapeKeyDown={(event) => {
            if (busy) event.preventDefault();
          }}
          onPointerDownOutside={(event) => {
            if (busy) event.preventDefault();
          }}
        >
          <DialogTitle>
            {edit?.action === "add"
              ? "添加设备"
              : edit?.action === "rename"
                ? "重命名设备"
                : "删除设备"}
          </DialogTitle>
          <DialogDescription>
            {edit?.action === "add"
              ? "沿用一台已有设备的线路与访问限制，生成新的独立订阅。不能增加管理员未授权的线路。"
              : edit?.action === "rename"
                ? "只修改显示名称，订阅地址和节点身份保持不变。"
                : `确认删除「${edit?.device?.label || edit?.device?.name || ""}」？旧订阅将立即失效，节点授权会自动撤销，已计入用户的用量保留。`}
          </DialogDescription>
          <form onSubmit={submit} className="space-y-5">
            {error && <Alert kind="error">{error}</Alert>}
            {edit && edit.action !== "delete" && (
              <div className="space-y-2">
                <label htmlFor="personal-device-name">设备名称</label>
                <Input
                  id="personal-device-name"
                  autoFocus
                  required
                  maxLength={64}
                  placeholder="例如：手机、家用电脑"
                  disabled={busy}
                  value={edit.name}
                  onChange={(e) => setEdit({ ...edit, name: e.target.value })}
                />
              </div>
            )}
            {edit?.action === "add" && (
              <div className="space-y-2">
                <label htmlFor="personal-device-source">
                  沿用哪台设备的线路与规则
                </label>
                <Select
                  disabled={busy}
                  value={edit.from}
                  onValueChange={(from) => setEdit({ ...edit, from })}
                >
                  <SelectTrigger id="personal-device-source" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {sources.map((d) => (
                      <SelectItem key={d.name} value={d.name}>
                        {d.label || d.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <p className="text-xs text-muted-foreground">
                  新设备共用你的流量池，限速及访问规则继续有效。
                </p>
              </div>
            )}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={busy}
                onClick={() => setDialogOpen(false)}
              >
                取消
              </Button>
              <Button
                type="submit"
                variant={edit?.action === "delete" ? "destructive" : "default"}
                disabled={
                  busy || (edit?.action !== "delete" && !edit?.name.trim())
                }
              >
                {busy
                  ? "正在保存…"
                  : edit?.action === "delete"
                    ? "确认删除设备"
                    : "保存设备"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}
