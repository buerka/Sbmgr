import { useState } from "react";
import { Link } from "react-router-dom";
import type { Context, Snapshot } from "../types";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "./ui/select";
import { Alert } from "./ui/feedback";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "./ui/dialog";
import { Icon } from "./Icons";
import { assignmentOptions } from "./routeModel";
import { useActionJob } from "./useActionJob";
import { restoreActionTrigger } from "./actionFocus";

export function RouteAssignment({
  snapshot,
  context,
  onClose,
}: {
  snapshot: Snapshot;
  context: Context;
  onClose: () => void;
}) {
  // Freeze the editing baseline, including its version, across live statistics refreshes.
  const [baseline] = useState(snapshot);
  const user = baseline.users.find((u) => u.name === context.user)!;
  const [device, setDevice] = useState(
    context.device || user?.devices[0]?.name || "",
  );
  const initial = (d: string) => [
    ...new Set(user.nodes.filter((n) => n.device === d).map((n) => n.outbound)),
  ];
  const [selected, setSelected] = useState(() => (user ? initial(device) : []));
  const [search, setSearch] = useState("");
  const [discard, setDiscard] = useState(false);
  const [nextDevice, setNextDevice] = useState<string | null>(null);
  const { submit, error, busy, blocked } = useActionJob(onClose);
  if (!user) return null;
  const original = initial(device);
  const added = selected.filter((o) => !original.includes(o));
  const removed = original.filter((o) => !selected.includes(o));
  const dirty = added.length + removed.length > 0;
  const options = assignmentOptions(baseline, user, device);
  const visible = options.filter((o) =>
    `${o.name} ${o.entry} ${o.path}`
      .toLowerCase()
      .includes(search.toLowerCase()),
  );
  const groups = [...new Set(visible.map((o) => o.entry))];
  const close = () => {
    if (!busy) {
      if (dirty) setDiscard(true);
      else onClose();
    }
  };
  const switchDevice = (value: string) => {
    setDevice(value);
    setSelected(initial(value));
    setSearch("");
    setNextDevice(null);
  };
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <DialogContent
        className="action-editor assignment-editor"
        showCloseButton={false}
        onCloseAutoFocus={(e) => {
          if (restoreActionTrigger()) e.preventDefault();
        }}
        onEscapeKeyDown={(e) => {
          e.preventDefault();
          close();
        }}
        onPointerDownOutside={(e) => {
          e.preventDefault();
          close();
        }}
      >
        <form
          className="editor-form"
          onSubmit={(e) => {
            e.preventDefault();
            void submit({
              action: "node.assign",
              confirm: false,
              fields: {
                user: user.name,
                device,
                expected:
                  user.devices.find((d) => d.name === device)
                    ?.assignment_version || "",
                selection: JSON.stringify(
                  selected.map((outbound) => ({
                    outbound,
                    name:
                      options.find((o) => o.outbound === outbound)?.name ||
                      "线路",
                  })),
                ),
              },
            });
          }}
        >
          <div className="editor-heading">
            <div className="flex justify-between items-start gap-4">
              <div>
                <DialogTitle>分配线路</DialogTitle>
                <DialogDescription className="mt-2">
                  为 {user.name} 选择可用线路，已分配的线路已勾选。
                </DialogDescription>
              </div>
              <Button
                variant="ghost"
                size="icon"
                aria-label="关闭"
                onClick={close}
                disabled={busy}
              >
                <Icon name="close" />
              </Button>
            </div>
            <div className="assignment-device-row mt-5 flex items-center gap-3">
              <label
                className="text-sm font-medium"
                htmlFor="assignment-device"
              >
                设备
              </label>
              <Select
                value={device}
                disabled={busy || !!context.device}
                onValueChange={(value) => {
                  if (dirty) setNextDevice(value);
                  else switchDevice(value);
                }}
              >
                <SelectTrigger
                  id="assignment-device"
                  className="assignment-device-select"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {user.devices.map((d) => (
                    <SelectItem key={d.name} value={d.name}>
                      {d.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <span className="ml-auto text-sm text-muted-foreground">
                已选 {selected.length} 条
              </span>
            </div>
            <Input
              className="mt-4"
              aria-label="搜索可分配线路"
              placeholder="搜索线路、入口或落地…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <div className="editor-content assignment-options">
            {baseline.mesh_pending && (
              <Alert kind="warning">
                拓扑尚未应用。已授权线路可以保留，新增授权请先在线路管理中应用拓扑。
              </Alert>
            )}
            {groups.map((group) => (
              <fieldset key={group} disabled={busy}>
                <legend>
                  {group}
                  <span>
                    {visible.filter((o) => o.entry === group).length} 条线路
                  </span>
                </legend>
                {visible
                  .filter((o) => o.entry === group)
                  .map((o) => {
                    const checked = selected.includes(o.outbound),
                      had = original.includes(o.outbound);
                    return (
                      <label
                        className={`assignment-option ${checked ? "is-selected" : ""}`}
                        key={o.outbound}
                      >
                        <input
                          type="checkbox"
                          checked={checked}
                          disabled={!had && !o.available}
                          aria-label={o.name}
                          onChange={(e) =>
                            setSelected((prev) =>
                              e.target.checked
                                ? [...prev, o.outbound]
                                : prev.filter((v) => v !== o.outbound),
                            )
                          }
                        />
                        <span className="min-w-0">
                          <strong>{o.name}</strong>
                          <small>{o.path}</small>
                        </span>
                        <span className="assignment-state">
                          {had
                            ? checked
                              ? "已分配"
                              : "将撤销"
                            : !o.available
                              ? "待应用"
                              : checked
                                ? "将新增"
                                : ""}
                        </span>
                      </label>
                    );
                  })}
              </fieldset>
            ))}
            {!visible.length && (
              <p className="py-8 text-center text-sm text-muted-foreground">
                没有匹配的线路。
                <Link
                  onClick={(event) => {
                    if (dirty) event.preventDefault();
                    close();
                  }}
                  to="/routes"
                  className="underline"
                >
                  前往线路管理
                </Link>
              </p>
            )}
          </div>
          <div className="editor-bottom">
            {error && <Alert kind="error">{error}</Alert>}
            {(discard || nextDevice !== null) && (
              <Alert kind="warning">
                <p>当前选择尚未保存。</p>
                <Button
                  variant="ghost"
                  onClick={() => {
                    setDiscard(false);
                    setNextDevice(null);
                  }}
                >
                  继续编辑
                </Button>
                <Button
                  variant="ghost"
                  onClick={() =>
                    nextDevice !== null ? switchDevice(nextDevice) : onClose()
                  }
                >
                  放弃修改
                </Button>
              </Alert>
            )}
            <p className="text-xs text-muted-foreground mb-3">
              仅修改此设备的线路授权。保留的节点沿用原名称、身份和限速；保存后需应用配置。
            </p>
            {removed.length > 0 && (
              <p className="text-sm mb-3 text-destructive">
                将撤销：
                {removed
                  .map((o) => options.find((r) => r.outbound === o)?.name || o)
                  .join("、")}
              </p>
            )}
            <div className="editor-footer">
              <span className="mr-auto text-xs text-muted-foreground">
                {dirty
                  ? `新增 ${added.length} · 撤销 ${removed.length}`
                  : "授权未改变"}
              </span>
              <Button variant="outline" onClick={close} disabled={busy}>
                取消
              </Button>
              <Button
                type="submit"
                disabled={!dirty || !selected.length || busy || blocked}
              >
                {busy ? "正在保存…" : "保存线路分配"}
              </Button>
            </div>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
