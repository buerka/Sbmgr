import { useState } from "react";
import { api } from "../api";
import type { Device } from "../types";
import { Button } from "./ui/button";
import { Alert } from "./ui/feedback";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "./ui/dialog";
import { notify, useAppDispatch } from "../store";

export function ResetSubscriptionLink({
  device,
  version,
  refresh,
}: {
  device: Device;
  version: string;
  refresh: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false),
    [expected, setExpected] = useState(""),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const dispatch = useAppDispatch();
  async function reset() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const reply = await api.selfDevice({
        action: "rotate-link",
        device: device.name,
        expected,
      });
      setOpen(false);
      dispatch(notify({ message: reply.message, severity: "success" }));
      await refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : "重置失败，请刷新后重试。");
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => {
          setExpected(version);
          setError("");
          setOpen(true);
        }}
      >
        重置链接
      </Button>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (!busy) setOpen(next);
        }}
      >
        <DialogContent
          className="sm:max-w-lg"
          onEscapeKeyDown={(e) => {
            if (busy) e.preventDefault();
          }}
          onPointerDownOutside={(e) => {
            if (busy) e.preventDefault();
          }}
        >
          <DialogTitle>重置订阅链接</DialogTitle>
          <DialogDescription>
            将为「{device.label || device.name}
            」生成新的订阅地址。旧链接立即失效，重置后请重新复制链接或下载
            TXT，并更新客户端中的订阅地址。
          </DialogDescription>
          <Alert>
            此操作只更换订阅链接，不更换节点身份。已经导入客户端的节点仍可使用；如果节点配置也已外泄，请联系管理员重建设备身份。
          </Alert>
          {error && <Alert kind="error">{error}</Alert>}
          <DialogFooter>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setOpen(false)}
            >
              取消
            </Button>
            <Button disabled={busy} onClick={() => void reset()}>
              {busy ? "正在重置…" : "确认重置链接"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
