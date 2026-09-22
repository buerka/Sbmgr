import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type PointerEvent,
} from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import { useAppDispatch, useAppSelector, refreshSnapshot } from "../store";
import type { RouteInventory, Snapshot } from "../types";
import { ActionButton, ActionMenu, Badge, Empty } from "./common";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Alert } from "./ui/feedback";
import { Icon } from "./Icons";
import {
  memberName,
  routeName,
  routePath,
  routeTag,
  routeDestination,
  clientExitName,
  canonicalOutbound,
} from "./routeModel";
import { useActionJob } from "./useActionJob";

type Route = Snapshot["routes"][number];
type Exit = RouteInventory["exits"][number];
type Draft = {
  id: string;
  exit: string;
  entry: string;
  revision: number;
  original?: Route;
};
const localRoute = (r: Route) => r.hops.length === 1 && r.hops[0] === r.entry;
function nextID(s: Snapshot, entry: string, exit: string) {
  const stem = `${entry}-${exit || "direct"}`
    .toLowerCase()
    .replace(/[^a-z0-9-]/g, "-")
    .slice(0, 27);
  let id = stem,
    i = 2;
  while (s.routes.some((r) => r.id === id)) id = `${stem}-${i++}`;
  return id;
}
export function RouteCanvas() {
  const s = useAppSelector((s) => s.admin.snapshot)!;
  const dispatch = useAppDispatch();
  const [entry, setEntry] = useState(
    s.members.find((m) => m.master || m.client)?.id || "",
  );
  const [selection, setSelection] = useState("");
  const [draft, setDraft] = useState<Draft | null>(null);
  const [cache, setCache] = useState<Record<string, Exit[]>>({});
  const [loading, setLoading] = useState(false);
  const [inventoryError, setInventoryError] = useState("");
  const [reload, setReload] = useState(0);
  const [search, setSearch] = useState("");
  const [connect, setConnect] = useState(false);
  const [point, setPoint] = useState<{ x: number; y: number } | null>(null);
  const [discard, setDiscard] = useState<(() => void) | null>(null);
  const canvas = useRef<HTMLDivElement>(null);
  const pointer = useRef<{ x: number; y: number; moved: boolean } | null>(null);
  const dragged = useRef(false);
  const [width, setWidth] = useState(760);
  const { submit, error, setError, busy, blocked } = useActionJob(() => {
    setSelection(draft?.id || "");
    setDraft(null);
    setConnect(false);
    void dispatch(refreshSnapshot());
  });
  const current = s.routes.find((r) => r.id === selection);
  const selected = draft?.original || current;
  const dirty =
    !!draft &&
    (!draft.original ||
      draft.exit !== draft.original.exit ||
      draft.entry !== draft.original.entry);
  const act = (fn: () => void) => {
    if (busy) return;
    if (dirty) setDiscard(() => fn);
    else {
      setDraft(null);
      setConnect(false);
      setError("");
      fn();
    }
  };
  useEffect(() => {
    if (!entry || s.role !== "master") return;
    let live = true;
    setLoading(true);
    setInventoryError("");
    api
      .routeInventory(entry)
      .then((result) => {
        if (live) setCache((old) => ({ ...old, [entry]: result.exits }));
      })
      .catch((e) => {
        if (live)
          setInventoryError(
            e instanceof Error ? e.message : "落地目录读取失败",
          );
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [entry, reload, s.role]);
  useLayoutEffect(() => {
    if (!canvas.current) return;
    const observer = new ResizeObserver((entries) =>
      setWidth(entries[0].contentRect.width),
    );
    observer.observe(canvas.current);
    return () => observer.disconnect();
  }, [entry, s.members.length]);
  useEffect(() => {
    if (!dirty) return;
    const guard = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", guard);
    return () => window.removeEventListener("beforeunload", guard);
  }, [dirty]);
  if (!s.members.length || s.role !== "master")
    return (
      <Empty
        title={s.role === "slave" ? "请在主机编排线路" : "先添加服务器"}
        description="在「服务器与落地」中登记入口和落地，然后回来连线。"
        icon="routes"
      />
    );
  const routes = s.routes.filter((r) => r.entry === entry);
  const filtered = routes.filter((r) =>
    `${routeName(r)} ${r.id}`.toLowerCase().includes(search.toLowerCase()),
  );
  const exits = [...(cache[entry] || [])];
  for (const r of routes.filter(localRoute))
    if (!exits.some((e) => e.tag === r.exit))
      exits.push({
        tag: r.exit,
        name: r.exit || "直接出站",
        type: r.exit ? "已配置" : "direct",
      });
  if (!exits.length) exits.push({ tag: "", name: "直接出站", type: "direct" });
  const label = memberName(s, entry);
  const height = Math.max(360, exits.length * 92 + 84);
  const start = { x: 224, y: height / 2 };
  const end = (i: number) => ({ x: width - 248, y: 68 + i * 92 });
  const path = (target: { x: number; y: number }) =>
    `M ${start.x} ${start.y} C ${start.x + (target.x - start.x) * 0.5} ${start.y}, ${target.x - (target.x - start.x) * 0.5} ${target.y}, ${target.x} ${target.y}`;
  function wire(exit: string) {
    if (busy || blocked || !cache[entry] || inventoryError) return;
    const existing = routes.find((r) => localRoute(r) && r.exit === exit);
    if (draft) {
      setDraft({ ...draft, exit });
    } else if (existing) {
      setSelection(existing.id);
      setDraft(null);
    } else {
      setSelection("");
      setDraft({
        id: nextID(s, entry, exit),
        entry,
        exit,
        revision: s.revision,
      });
    }
    setConnect(false);
    setPoint(null);
    setError("");
  }
  const move = (e: PointerEvent<HTMLDivElement>) => {
    if (!pointer.current || !canvas.current) return;
    pointer.current.moved ||=
      Math.hypot(e.clientX - pointer.current.x, e.clientY - pointer.current.y) >
      5;
    const box = canvas.current.getBoundingClientRect();
    setPoint({ x: e.clientX - box.left, y: e.clientY - box.top });
  };
  const finish = (e: PointerEvent<HTMLDivElement>) => {
    if (!pointer.current) return;
    const moved = pointer.current.moved;
    dragged.current = moved;
    pointer.current = null;
    setPoint(null);
    if (moved) {
      const target = document
        .elementFromPoint(e.clientX, e.clientY)
        ?.closest<HTMLElement>("[data-wire-exit]");
      if (target && canvas.current?.contains(target))
        wire(target.dataset.wireExit || "");
    }
  };
  const activeExit = draft
    ? draft.exit
    : selected && localRoute(selected)
      ? selected.exit
      : undefined;
  const users = selected
    ? s.users.filter((u) =>
        u.nodes.some(
          (n) => canonicalOutbound(s, n.outbound) === routeTag(selected.id),
        ),
      )
    : [];
  const assignments = users.map((user) => ({
    user,
    devices: [
      ...new Set(
        user.nodes
          .filter(
            (node) =>
              canonicalOutbound(s, node.outbound) ===
              routeTag(selected!.id),
          )
          .map((node) => node.device),
      ),
    ],
  }));
  return (
    <div className="route-studio">
      <div className="route-studio-toolbar">
        <div className="entry-switch" aria-label="选择入口服务器">
          {s.members
            .filter((m) => m.master || m.client)
            .map((m) => (
              <Button
                key={m.id}
                variant={m.id === entry ? "secondary" : "ghost"}
                size="sm"
                aria-pressed={m.id === entry}
                aria-label={`入口：${memberName(s, m.id)}`}
                onClick={() =>
                  act(() => {
                    setEntry(m.id);
                    setSelection("");
                  })
                }
              >
                <Icon name="server" />
                {memberName(s, m.id)}
                <span className="entry-count">
                  {s.routes.filter((r) => r.entry === m.id).length}
                </span>
              </Button>
            ))}
        </div>
        <Button
          size="sm"
          onClick={() =>
            act(() => {
              setSelection("");
              setDraft(null);
              setConnect(true);
            })
          }
          disabled={busy || blocked}
        >
          <Icon name="add" />
          新建线路
        </Button>
      </div>
      <div className="route-studio-body">
        <aside className="route-library">
          <div className="route-library-heading">
            <h2>
              已保存线路 <span>{routes.length}</span>
            </h2>
            <Input
              aria-label="搜索已保存线路"
              placeholder="搜索线路…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <div className="route-library-list">
            {filtered.map((r) => (
              <button
                key={r.id}
                className={`route-list-item ${selection === r.id || draft?.original?.id === r.id ? "is-selected" : ""}`}
                aria-pressed={selection === r.id}
                onClick={() => act(() => setSelection(r.id))}
              >
                <Icon name="routes" />
                <span>
                  <strong>{routeName(r)}</strong>
                  <small>
                    {routePath(s, r)}
                    {!localRoute(r) && " · 多跳"}
                  </small>
                </span>
                <Icon name="next" size={14} />
              </button>
            ))}
            {!filtered.length && (
              <p className="p-5 text-xs text-muted-foreground">
                {search
                  ? "没有匹配的线路"
                  : "从入口圆点拖线到落地，建立第一条线路。"}
              </p>
            )}
          </div>
          <div className="route-library-footer">
            <Icon name="users" />
            <p>
              连线保存并应用后，<Link to="/users">前往用户管理分配</Link>。
            </p>
          </div>
        </aside>
        <div className="route-workarea">
          <div className="canvas-heading">
            <div>
              <h2>{label} 的线路</h2>
              <p>
                {connect
                  ? "选择右侧落地完成连线，也可以从入口圆点拖动。"
                  : "拖动入口圆点连接落地；点击已有线路查看或修改。"}
              </p>
            </div>
            <Button
              variant="ghost"
              size="icon"
              aria-label="刷新落地目录"
              title="刷新落地目录"
              disabled={loading}
              onClick={() => setReload((n) => n + 1)}
            >
              <Icon name="refresh" className={loading ? "animate-spin" : ""} />
            </Button>
          </div>
          {inventoryError && (
            <div className="px-5">
              <Alert kind="warning">{inventoryError}</Alert>
            </div>
          )}
          <div className="wire-scroll">
            <div
              ref={canvas}
              className={`wire-canvas ${connect ? "is-connecting" : ""}`}
              style={{ height }}
              onPointerMove={move}
              onPointerUp={finish}
              onPointerCancel={() => {
                pointer.current = null;
                setPoint(null);
              }}
            >
              <span className="canvas-column-label canvas-entry-label">
                客户端入口
              </span>
              <span className="canvas-column-label canvas-exit-label">
                {label} 上的落地
              </span>
              <svg
                className="wire-layer"
                width="100%"
                height={height}
                aria-hidden="true"
              >
                {routes.filter(localRoute).map((r) => {
                  const index = exits.findIndex((e) => e.tag === r.exit);
                  if (index < 0 || draft?.original?.id === r.id) return null;
                  return (
                    <path
                      key={r.id}
                      d={path(end(index))}
                      className={`route-wire ${selection === r.id ? "active" : ""}`}
                      onClick={() => act(() => setSelection(r.id))}
                    />
                  );
                })}
                {draft && exits.findIndex((e) => e.tag === draft.exit) >= 0 && (
                  <path
                    d={path(end(exits.findIndex((e) => e.tag === draft.exit)))}
                    className="route-wire draft"
                  />
                )}
                {point && <path d={path(point)} className="route-wire draft" />}
              </svg>
              <div className="wire-entry" style={{ top: start.y - 46 }}>
                <span className="wire-node-icon">
                  <Icon name="server" size={22} />
                </span>
                <strong>{label}</strong>
                <small>
                  {s.members.find((m) => m.id === entry)?.master
                    ? "主机入口"
                    : "独立从机入口"}
                </small>
                <button
                  className="wire-handle source"
                  aria-label={`从 ${label} 连线`}
                  aria-pressed={connect}
                  disabled={
                    busy || blocked || !cache[entry] || !!inventoryError
                  }
                  onClick={() => {
                    if (dragged.current) {
                      dragged.current = false;
                      return;
                    }
                    setConnect(true);
                  }}
                  onPointerDown={(e) => {
                    if (e.button !== 0) return;
                    pointer.current = {
                      x: e.clientX,
                      y: e.clientY,
                      moved: false,
                    };
                    e.currentTarget.setPointerCapture(e.pointerId);
                    setConnect(true);
                  }}
                />
              </div>
              {exits.map((exit, i) => {
                const existing = routes.find(
                  (r) => localRoute(r) && r.exit === exit.tag,
                );
                const niceName = existing
                  ? routeDestination(existing)
                  : clientExitName(exit.tag, exit.name);
                return (
                  <button
                    key={exit.tag}
                    data-wire-exit={exit.tag}
                    className={`wire-exit ${activeExit === exit.tag ? "active" : ""}`}
                    style={{ top: end(i).y - 34 }}
                    aria-label={`落地：${niceName}`}
                    onClick={() => {
                      if (connect || draft?.original) wire(exit.tag);
                      else if (existing) act(() => setSelection(existing.id));
                      else wire(exit.tag);
                    }}
                    disabled={busy}
                  >
                    <span className="wire-handle target" />
                    <span className="wire-exit-icon">
                      <Icon
                        name={exit.type === "direct" ? "globe" : "routes"}
                        size={17}
                      />
                    </span>
                    <span>
                      <strong>{niceName}</strong>
                      <small>
                        {exit.type === "direct"
                          ? "服务器公网出口"
                          : `${exit.type.toUpperCase()} · ${label}`}
                      </small>
                    </span>
                    <span
                      className={`wire-status ${existing ? "connected" : ""}`}
                    >
                      {existing ? (
                        <Icon name="check" size={14} />
                      ) : (
                        <Icon name="add" size={14} />
                      )}
                    </span>
                  </button>
                );
              })}
            </div>
          </div>
          <div className="canvas-legend">
            <span>
              <i className="legend-connected" />
              已保存
            </span>
            <span>
              <i className="legend-draft" />
              待保存连线
            </span>
            <p>连线只使用当前入口所在服务器的落地。</p>
          </div>
          {draft || selected ? (
            <div className="route-inspector">
              <div className="flex items-start justify-between gap-3">
                <div>
                  <span className="eyebrow">
                    {draft
                      ? draft.original
                        ? "修改连线"
                        : "新建线路"
                      : "线路详情"}
                  </span>
                  <h3>
                    {selected
                      ? routeName(selected)
                      : draft?.exit
                        ? `${clientExitName(draft.exit)} via ${label}`
                        : label}
                  </h3>
                  <p className="text-xs text-muted-foreground mt-1">
                    {draft
                      ? `${label} → ${clientExitName(draft.exit)}`
                      : selected && routePath(s, selected)}
                  </p>
                </div>
                <Badge
                  kind={
                    draft ? "warning" : s.mesh_pending ? "warning" : "success"
                  }
                >
                  {draft ? "未保存" : s.mesh_pending ? "待应用拓扑" : "已应用"}
                </Badge>
              </div>
              {draft ? (
                <form
                  className="wire-save-form"
                  onSubmit={(e) => {
                    e.preventDefault();
                    void submit({
                      action: "mesh.wire",
                      confirm: false,
                      fields: {
                        id: draft.id,
                        entry: draft.entry,
                        exit: draft.exit,
                        revision: String(draft.revision),
                        replace: String(!!draft.original),
                      },
                    });
                  }}
                >
                  <div>
                    <label htmlFor="wire-id">线路标识</label>
                    <Input
                      id="wire-id"
                      value={draft.id}
                      readOnly={!!draft.original}
                      required
                      pattern="[a-z][a-z0-9-]{0,31}"
                      maxLength={32}
                      onChange={(e) =>
                        setDraft({ ...draft, id: e.target.value })
                      }
                    />
                    <p className="field-hint">
                      小写字母、数字和连字符。已有标识保持不变。
                    </p>
                  </div>
                  <Button
                    variant="outline"
                    disabled={busy}
                    onClick={() => act(() => setDraft(null))}
                  >
                    取消
                  </Button>
                  <Button type="submit" disabled={busy || blocked || !dirty}>
                    {busy ? "保存中…" : "保存线路"}
                  </Button>
                </form>
              ) : (
                <div className="route-inspector-actions">
                  <div className="route-assignees">
                    <div className="route-assignees-heading">
                      <span>已分配用户</span>
                      <strong>{assignments.length}</strong>
                    </div>
                    {assignments.length ? (
                      <ul>
                        {assignments.map(({ user, devices }) => (
                          <li key={user.name}>
                            <Link
                              to={`/users/${encodeURIComponent(user.name)}`}
                            >
                              {user.name}
                            </Link>
                            <small>{devices.join("、")}</small>
                          </li>
                        ))}
                      </ul>
                    ) : (
                      <p>尚未分配给用户</p>
                    )}
                  </div>
                  {selected && localRoute(selected) ? (
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={busy || !cache[entry] || !!inventoryError}
                      onClick={() => {
                        setDraft({
                          id: selected.id,
                          entry,
                          exit: selected.exit,
                          original: selected,
                          revision: s.revision,
                        });
                        setConnect(true);
                      }}
                    >
                      <Icon name="edit" />
                      修改连线
                    </Button>
                  ) : (
                    selected && (
                      <ActionButton
                        id="mesh.route"
                        context={{ id: selected.id }}
                      >
                        编辑多跳线路
                      </ActionButton>
                    )
                  )}
                  {selected && (
                    <ActionMenu
                      compact
                      label={`管理线路：${routeName(selected)}`}
                      items={[
                        {
                          id: "mesh.remove-route",
                          context: { id: selected.id },
                        },
                      ]}
                    />
                  )}
                </div>
              )}
              {draft?.original && users.length > 0 && (
                <p className="text-xs text-amber-600 dark:text-amber-400 mt-3">
                  应用后，这 {users.length}{" "}
                  位用户的该线路会切换至新的落地，节点身份与名称保留。
                </p>
              )}
              {error && <Alert kind="error">{error}</Alert>}
            </div>
          ) : (
            <div className="canvas-empty-hint">
              <Icon name="routes" />
              <p>选择一条线路查看详情，或连接右侧落地创建线路。</p>
            </div>
          )}
          {discard && (
            <div className="p-5 border-t">
              <Alert kind="warning">
                <p>当前连线尚未保存。</p>
                <Button variant="ghost" onClick={() => setDiscard(null)}>
                  继续编辑
                </Button>
                <Button
                  variant="ghost"
                  onClick={() => {
                    const next = discard;
                    setDraft(null);
                    setConnect(false);
                    setDiscard(null);
                    next();
                  }}
                >
                  放弃修改
                </Button>
              </Alert>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
