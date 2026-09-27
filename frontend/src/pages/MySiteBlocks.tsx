import { useEffect, useState, type FormEvent } from "react";
import { api } from "../api";
import type { SiteBlocksSnapshot } from "../types";
import { Empty, PageHeader, Panel } from "../components/common";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Alert } from "../components/ui/feedback";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "../components/ui/dialog";

export function MySiteBlocks() {
  const [data, setData] = useState<SiteBlocksSnapshot | null>(null);
  const [input, setInput] = useState("");
  const [removing, setRemoving] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  async function refresh() {
    setRefreshing(true);
    try {
      setData(await api.siteBlocks());
      setError("");
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "读取网站屏蔽列表失败。",
      );
    } finally {
      setRefreshing(false);
    }
  }
  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => {
      if (!document.hidden) void refresh();
    }, 30000);
    return () => window.clearInterval(timer);
  }, []);

  async function change(action: "add" | "remove", domain: string) {
    if (!data || busy) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const next = await api.updateSiteBlocks({
        action,
        domain,
        expected: data.version,
      });
      setData(next);
      setNotice(next.message || "已保存，规则将由后台自动应用。");
      if (action === "add") setInput("");
      setRemoving(null);
    } catch (reason) {
      await refresh();
      setError(
        reason instanceof Error ? reason.message : "保存失败，请刷新后重试。",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title="网站屏蔽"
        description="屏蔽指定域名及其子域名，作用于你账号下所有设备的代理访问。"
        actions={
          <Button
            variant="outline"
            disabled={refreshing || busy}
            onClick={() => void refresh()}
          >
            刷新状态
          </Button>
        }
      />
      <div className="space-y-5">
        <Alert>
          保存后通常在约 1
          分钟内自动应用，各入口完成时间可能不同。应用失败会重试，保存失败则保持原设置。管理员的访问限制仍然生效。
        </Alert>
        {data?.pending && (
          <Alert kind="warning">
            当前仍有配置待应用。列表显示已保存的规则，不表示所有入口已经生效；持续未完成请联系管理员。
          </Alert>
        )}
        {notice && <Alert kind="success">{notice}</Alert>}
        {error && <Alert kind="error">{error}</Alert>}
        <Panel
          title="添加网站"
          description="输入域名或完整网站地址。只屏蔽域名及子域名，不区分页面路径；不支持目标 IP，也不影响客户端直连。"
        >
          <div className="px-6 pb-6">
            <form
              className="flex flex-col sm:flex-row gap-3 sm:items-end"
              onSubmit={(event: FormEvent) => {
                event.preventDefault();
                void change("add", input.trim());
              }}
            >
              <div className="space-y-1 flex-1 min-w-0">
                <label htmlFor="site-block-domain" className="text-sm">
                  域名或网站地址
                </label>
                <Input
                  id="site-block-domain"
                  value={input}
                  onChange={(event) => setInput(event.target.value)}
                  placeholder="example.org 或 https://example.org/page"
                  autoComplete="off"
                  required
                  maxLength={2048}
                  disabled={!data || busy}
                />
              </div>
              <Button
                type="submit"
                disabled={
                  !data ||
                  busy ||
                  !input.trim() ||
                  data.domains.length >= data.limit
                }
              >
                添加屏蔽
              </Button>
            </form>
            {data && (
              <p className="text-sm text-muted-foreground mt-3">
                已使用 {data.domains.length} / {data.limit} 个名额。
              </p>
            )}
          </div>
        </Panel>
        <Panel
          title="已屏蔽网站"
          description="移除后将由后台自动应用；管理员另行设置的限制不会被解除。"
        >
          <div className="px-6 pb-6">
            {!data ? (
              <p role="status" className="text-sm text-muted-foreground">
                正在读取网站屏蔽列表…
              </p>
            ) : data.domains.length ? (
              <ul className="divide-y">
                {data.domains.map((domain) => (
                  <li
                    key={domain}
                    className="flex flex-wrap items-center justify-between gap-3 py-3"
                  >
                    <span className="font-medium break-all min-w-0">
                      {domain}
                    </span>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={busy}
                      onClick={() => setRemoving(domain)}
                      aria-label={`移除 ${domain} 的屏蔽`}
                    >
                      移除
                    </Button>
                  </li>
                ))}
              </ul>
            ) : (
              <Empty
                title="尚未屏蔽网站"
                description="可在这里添加网站，也可从数据看板选择网站屏蔽。"
              />
            )}
          </div>
        </Panel>
      </div>
      <Dialog
        open={removing !== null}
        onOpenChange={(open) => {
          if (!open && !busy) setRemoving(null);
        }}
      >
        <DialogContent>
          <DialogTitle>移除网站屏蔽</DialogTitle>
          <DialogDescription>
            确认移除「{removing}
            」？你账号下所有设备将在后台应用后不再受这条个人规则限制，管理员规则仍然生效。
          </DialogDescription>
          <DialogFooter>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setRemoving(null)}
            >
              取消
            </Button>
            <Button
              disabled={busy}
              onClick={() => {
                if (removing) void change("remove", removing);
              }}
            >
              确认移除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
