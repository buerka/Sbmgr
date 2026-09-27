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
      dispatch(
        notify({
          message: reply.pending
            ? "旧订阅地址已失效，本机正在应用新配置。旧客户端配置须待各入口应用后失效；请重新获取并导入新订阅。"
            : "旧订阅地址已失效。旧客户端配置须待各入口应用后失效；请重新获取并导入新订阅。",
          severity: reply.pending ? "info" : "success",
        }),
      );
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
        重置订阅
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
          <DialogTitle>重置订阅与连接配置</DialogTitle>
          <DialogDescription>
            将为「{device.label || device.name}
            」生成新的订阅地址和连接配置。旧订阅地址立即失效；请重新获取并导入新订阅。
          </DialogDescription>
          <Alert>
            旧客户端配置须待各入口自动应用完成后失效。应用期间可能暂时仍能连接；各入口完成时间可能不同。
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
              {busy ? "正在重置…" : "确认重置订阅"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
