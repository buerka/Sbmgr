import { useEffect, useState, type FormEvent } from "react";
import { api } from "../api";
import {
  closeAction,
  jobReceived,
  useAppDispatch,
  useAppSelector,
} from "../store";
import type { Action, Context, Field, Snapshot } from "../types";
import {
  changedFields,
  collectFields,
  fieldOptions,
  initialFields,
  isEdit,
  listFields,
  lockedField,
  optionLabels,
} from "./formModel";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "./ui/dialog";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "./ui/select";
import { Switch } from "./ui/switch";
import { Alert } from "./ui/feedback";
import { Icon } from "./Icons";
import { Badge } from "./common";
import { restoreActionTrigger } from "./actionFocus";
import { RouteAssignment } from "./RouteAssignment";
const hints: Record<string, string> = {
  quota: "单位 G / M / T，填写 0 表示不限流量。",
  "extra-quota": "仅增加本账期的额度；0 表示没有附加流量。",
  "up-mbps": "Mbps，0 表示不限。",
  "down-mbps": "Mbps，0 表示不限。",
  expire: "留空为长期有效，清空已有日期会移除到期限制。",
  "billing-day": "每月 1–28 日，按已配置的账期时区重置用量。",
  "ip-max": "动态单活只能为 1；固定名单和自动学习可设置多个。",
  "ip-handover-seconds": "新来源替换旧来源之前的等待时间，单位为秒。",
  "ip-temp-minutes": "显示当前剩余分钟数；修改此值将从保存时重新计时。",
  "burst-limit": "窗口内累计流量达到该值时触发保护，如 2G。",
  "tier1-speed": "触发第一档后保留的原速率百分比。",
  "tier2-speed": "触发第二档后保留的原速率百分比。",
  hops: "依次填写服务器标识，以逗号分隔。",
  exit: "留空表示末跳服务器直接出站。",
  json: "提交后清空内容；已有密码与私钥不会回显。",
};
const groups: Record<string, [string, string[]][]> = {
  "user.set": [
    ["流量与计费", ["quota", "quota-mode", "extra-quota"]],
    ["有效期与账期", ["expire", "billing-enabled", "billing-day"]],
    ["速度上限", ["up-mbps", "down-mbps"]],
  ],
  "user.ip": [
    [
      "来源控制",
      [
        "ip-enabled",
        "ip-mode",
        "ip-binding",
        "ip-max",
        "ip-handover-seconds",
        "ip-allowed",
      ],
    ],
    ["临时放行", ["ip-temp", "ip-temp-minutes"]],
  ],
  "user.burst": [
    ["检测条件", ["burst-enabled", "burst-window", "burst-limit"]],
    [
      "触发后的处理",
      [
        "burst-action",
        "burst-block",
        "burst-soft-up-kbps",
        "burst-soft-down-kbps",
      ],
    ],
  ],
  "user.throttle": [
    ["限速开关", ["tiered"]],
    ["第一档", ["tier1-usage", "tier1-speed"]],
    ["第二档", ["tier2-usage", "tier2-speed"]],
  ],
  "subscription.set": [
    ["服务地址", ["enabled", "listen", "base-url"]],
    ["更新证书（可选）", ["tls-cert", "tls-key"]],
  ],
};
function label(field: Field) {
  return field.label.replace(/[（(].*?[）)]/g, "").trim();
}
function display(value: string, field: Field) {
  return field.type === "secret-text"
    ? "私有配置"
    : optionLabels[value] ||
        value ||
        (listFields.has(field.key) ? "无规则" : "未设置");
}

export function ActionDialog() {
  const { dialog, catalog, snapshot } = useAppSelector((s) => s.admin),
    dispatch = useAppDispatch();
  const action = catalog.find((a) => a.id === dialog?.id);
  if (!dialog || !action || !snapshot) return null;
  if (dialog.id === "node.assign")
    return (
      <RouteAssignment
        key={JSON.stringify(dialog)}
        snapshot={snapshot}
        context={dialog.context}
        onClose={() => dispatch(closeAction())}
      />
    );
  return (
    <ActionForm
      key={JSON.stringify(dialog)}
      action={action}
      context={dialog.context}
      snapshot={snapshot}
      onClose={() => dispatch(closeAction())}
    />
  );
}
export function ActionForm({
  action,
  context,
  snapshot,
  onClose,
}: {
  action: Action;
  context: Context;
  snapshot: Snapshot;
  onClose: () => void;
}) {
  const [scope, setScope] = useState(context),
    [initial, setInitial] = useState(() =>
      initialFields(action, context, snapshot),
    );
  const [values, setValues] = useState<Context>(initial),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [discard, setDiscard] = useState(false),
    [submittedJob, setSubmittedJob] = useState<string | null>(null);
  const job = useAppSelector((s) => s.admin.job);
  const dispatch = useAppDispatch(),
    edit = isEdit(action, context),
    changed = changedFields(action, values, initial, context),
    dirty = changed.length > 0;
  useEffect(() => {
    if (!submittedJob || job?.id !== submittedJob || job.status === "running")
      return;
    setSubmittedJob(null);
    setBusy(false);
    if (job.status === "success") {
      setValues({});
      onClose();
    } else setError(job.message || "保存失败，你的修改仍保留在表单中。");
  }, [job, submittedJob, onClose]);
  const setField = (field: Field, value: string) => {
    setError("");
    setDiscard(false);
    if (["user", "device", "node"].includes(field.key) && field.source) {
      const next = { ...scope, [field.key]: value };
      if (field.key === "user") {
        delete next.device;
        delete next.node;
      }
      if (field.key === "device") delete next.node;
      const populated = initialFields(action, next, snapshot);
      setScope(next);
      setInitial(populated);
      setValues(populated);
    } else setValues((old) => ({ ...old, [field.key]: value }));
  };
  const close = () => {
    if (!busy) {
      if (dirty) setDiscard(true);
      else onClose();
    }
  };
  async function submit(event: FormEvent) {
    event.preventDefault();
    setError("");
    const fields = collectFields(action, values, initial, context);
    const missing = action.fields.find(
      (f) => f.required && !fields[f.key]?.trim(),
    );
    if (missing) {
      setError(`请填写${label(missing)}`);
      return;
    }
    const emptyNumber = changed.find(
      (f) =>
        (f.type === "number" ||
          ["quota", "extra-quota", "burst-limit"].includes(f.key)) &&
        !values[f.key]?.trim(),
    );
    if (emptyNumber) {
      setError(`请填写${label(emptyNumber)}，不设限制时填写 0。`);
      return;
    }
    if (edit && !dirty) return;
    setBusy(true);
    try {
      const result = await api.action({
        action: action.id,
        fields,
        confirm: action.danger,
      });
      setValues((old) => ({
        ...old,
        ...Object.fromEntries(
          action.fields
            .filter((f) => f.type === "secret-text")
            .map((f) => [f.key, ""]),
        ),
      }));
      setSubmittedJob(result.id);
      dispatch(jobReceived(result));
    } catch (err) {
      setBusy(false);
      setError(
        err instanceof Error
          ? err.message
          : "保存失败，请重试。你的修改仍保留在表单中。",
      );
    }
  }
  function renderField(field: Field) {
    if (lockedField(field, context) || field.key === "clear-expire")
      return null;
    const value = values[field.key] || "",
      modified = value !== (initial[field.key] || ""),
      id = `field-${field.key}`,
      helpId = `help-${field.key}`;
    const helper =
      edit && modified && field.type !== "secret-text"
        ? `原值：${display(initial[field.key] || "", field)}`
        : hints[field.key] ||
          (listFields.has(field.key)
            ? "多项用逗号分隔；清空即可移除。"
            : field.label.match(/[（(](.*?)[）)]/)?.[1]);
    const bool =
      field.options?.length === 2 &&
      field.options.includes("true") &&
      field.options.includes("false");
    if (bool || field.type === "checkbox")
      return (
        <div className="form-row" key={field.key}>
          <label htmlFor={id}>{label(field)}</label>
          <div>
            <div className="flex items-center gap-3 h-9">
              <Switch
                id={id}
                aria-label={label(field)}
                checked={value === "true"}
                disabled={busy}
                onCheckedChange={(v) => setField(field, String(v))}
              />
              <span className="text-sm text-muted-foreground">
                {value === "true" ? "已开启" : "已关闭"}
              </span>
            </div>
            {modified && edit && (
              <p className="field-hint">
                原值：{display(initial[field.key] || "false", field)}
              </p>
            )}
          </div>
        </div>
      );
    const opts = fieldOptions(field, values, snapshot),
      multi = field.type === "multi-select",
      privatePath = ["tls-key", "tls-cert"].includes(field.key);
    return (
      <div className="form-row" key={field.key}>
        <label htmlFor={id}>
          {label(field)}
          {field.required && (
            <span className="text-destructive ml-1" aria-hidden="true">
              *
            </span>
          )}
        </label>
        <div className="min-w-0">
          {multi ? (
            <fieldset
              id={id}
              aria-label={label(field)}
              aria-describedby={helpId}
              className="multi-options"
              disabled={busy}
            >
              {opts.length ? (
                opts.map(([v, l]) => (
                  <label key={v} className="flex gap-2 items-center">
                    <input
                      type="checkbox"
                      checked={value.split(",").includes(v)}
                      onChange={(e) =>
                        setField(
                          field,
                          (e.target.checked
                            ? [...value.split(",").filter(Boolean), v]
                            : value.split(",").filter((i) => i !== v)
                          ).join(","),
                        )
                      }
                    />
                    {l}
                  </label>
                ))
              ) : (
                <span className="text-muted-foreground text-sm">
                  暂无可选项
                </span>
              )}
            </fieldset>
          ) : field.type === "select" ? (
            <Select
              value={value || "__empty__"}
              onValueChange={(v) => setField(field, v === "__empty__" ? "" : v)}
              disabled={busy}
            >
              <SelectTrigger
                id={id}
                aria-label={label(field)}
                aria-describedby={helper ? helpId : undefined}
                className="w-full"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__empty__">
                  {field.required
                    ? "请选择"
                    : field.source === "outbounds"
                      ? "默认直出"
                      : edit
                        ? "未设置"
                        : "使用默认值"}
                </SelectItem>
                {opts
                  .filter(([v]) => v)
                  .map(([v, l]) => (
                    <SelectItem key={v} value={v}>
                      {l}
                    </SelectItem>
                  ))}
              </SelectContent>
            </Select>
          ) : field.type === "secret-text" ? (
            <textarea
              id={id}
              aria-describedby={helpId}
              value={value}
              rows={7}
              autoComplete="off"
              spellCheck={false}
              maxLength={131072}
              required={field.required}
              disabled={busy}
              className="text-input font-mono text-xs"
              placeholder="粘贴新的单个 JSON 对象"
              onChange={(e) => setField(field, e.target.value)}
            />
          ) : (
            <Input
              id={id}
              aria-describedby={helper || privatePath ? helpId : undefined}
              value={value}
              required={field.required}
              disabled={busy}
              type={
                field.type === "number"
                  ? "number"
                  : field.type === "date"
                    ? "date"
                    : "text"
              }
              min={field.type === "number" ? 0 : undefined}
              step={field.type === "number" ? "any" : undefined}
              autoComplete="off"
              maxLength={131072}
              onChange={(e) => setField(field, e.target.value)}
            />
          )}
          {(helper || privatePath) && (
            <p id={helpId} className="field-hint">
              {privatePath
                ? snapshot.subscription.tls_configured
                  ? "已配置。留空保留，仅在更换证书时填写新路径。"
                  : "尚未配置，仅填写服务器上的文件路径。"
                : helper}
            </p>
          )}
        </div>
      </div>
    );
  }
  const grouped = groups[action.id] || [
    [
      "设置",
      action.fields
        .filter((f) => !f.source || !["user", "device", "node"].includes(f.key))
        .map((f) => f.key),
    ] as [string, string[]],
  ];
  const groupedKeys = new Set(grouped.flatMap((g) => g[1])),
    ungrouped = action.fields.filter(
      (f) =>
        !groupedKeys.has(f.key) &&
        f.key !== "clear-expire" &&
        !lockedField(f, context),
    );
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <DialogContent
        className="action-editor"
        onCloseAutoFocus={(event) => {
          if (restoreActionTrigger()) event.preventDefault();
        }}
        showCloseButton={false}
        onEscapeKeyDown={(e) => {
          e.preventDefault();
          close();
        }}
        onPointerDownOutside={(e) => {
          e.preventDefault();
          close();
        }}
      >
        <form onSubmit={submit} className="editor-form">
          <div className="editor-heading">
            <div className="flex items-start justify-between gap-4">
              <div>
                <DialogTitle>{action.title}</DialogTitle>
                <DialogDescription className="mt-2">
                  {edit
                    ? "修改当前设置，完成后保存。"
                    : action.danger
                      ? "请确认操作对象与生效范围。"
                      : "填写以下信息，完成后提交。"}
                </DialogDescription>
              </div>
              <Button
                variant="ghost"
                size="icon"
                aria-label="关闭"
                disabled={busy}
                onClick={close}
                className="-mr-2 -mt-2"
              >
                <Icon name="close" />
              </Button>
            </div>
            <div className="flex items-center gap-2 mt-4">
              <Badge kind="default">
                {[scope.user, scope.device, scope.node]
                  .filter(Boolean)
                  .join(" / ") ||
                  context.id ||
                  "当前工作空间"}
              </Badge>
              {edit && (
                <span className="text-xs text-muted-foreground">
                  已载入当前设置
                </span>
              )}
            </div>
          </div>
          <div className="editor-content">
            <div className="effect-note">
              <Icon name={action.danger ? "warning" : "shield"} />
              <p>
                {action.effect
                  .replace(/^已保存拓扑；/, "保存后，")
                  .replace(/^已保存；/, "保存后，")
                  .replace(/^设置已保存；/, "保存后，")}
              </p>
            </div>
            {ungrouped.length > 0 && (
              <div className="form-section">{ungrouped.map(renderField)}</div>
            )}
            {grouped.map(([title, keys]) => {
              const fields = action.fields.filter(
                (f) =>
                  keys.includes(f.key) &&
                  f.key !== "clear-expire" &&
                  !lockedField(f, context),
              );
              return (
                fields.length > 0 && (
                  <section className="form-section" key={title}>
                    {grouped.length > 1 && <h3>{title}</h3>}
                    {fields.map(renderField)}
                  </section>
                )
              );
            })}
            {action.fields.some((f) => f.type === "secret-text") && (
              <p className="field-hint">
                已有密码与私钥不会回显，请填写新的配置。
              </p>
            )}
          </div>
          <div className="editor-bottom">
            {error && <Alert kind="error">{error}</Alert>}
            {discard && (
              <Alert kind="warning">
                <p>有尚未保存的修改。继续编辑，或放弃后关闭。</p>
                <Button variant="ghost" onClick={onClose} className="mt-2">
                  放弃修改
                </Button>
              </Alert>
            )}
            <div className="editor-footer">
              <span className="text-xs text-muted-foreground mr-auto">
                {busy
                  ? "正在保存，请稍候…"
                  : edit
                    ? dirty
                      ? `已修改 ${changed.length} 项`
                      : "尚未修改"
                    : ""}
              </span>
              <Button variant="outline" onClick={close} disabled={busy}>
                取消
              </Button>
              <Button
                variant={action.danger ? "destructive" : "default"}
                type="submit"
                disabled={busy || (edit && !dirty)}
              >
                {busy && <Icon name="loading" className="animate-spin" />}
                {busy
                  ? "正在保存…"
                  : edit
                    ? "保存修改"
                    : action.danger
                      ? "确认操作"
                      : /\.(add|init|clone)$/.test(action.id)
                        ? "创建"
                        : "执行"}
              </Button>
            </div>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
