import {
  ActionButton,
  ActionMenu,
  Badge,
  DataTable,
  Empty,
  PageHeader,
  Panel,
} from "../components/common";
import { Alert } from "../components/ui/feedback";
import { TableCell, TableRow } from "../components/ui/table";
import { Icon } from "../components/Icons";
import { useAppSelector } from "../store";
export function RoutesPage() {
  const s = useAppSelector((s) => s.admin.snapshot)!;
  return (
    <>
      <PageHeader
        title="线路管理"
        description="管理客户端入口、转发路径与最终落地。"
        actions={
          <>
            <ActionButton id="mesh.check" />
            <ActionButton id="mesh.sync" />
            <ActionButton id="mesh.apply" variant="default" />
          </>
        }
      />
      {s.mesh_pending && (
        <Alert kind="warning">
          拓扑有待生效修改或未完成事务。应用拓扑后再分配节点；异常事务可在下方恢复。
        </Alert>
      )}
      <Panel
        title="服务器"
        description={`拓扑修订 ${s.revision}`}
        actions={
          <>
            {!s.members.length && <ActionButton id="mesh.init" />}
            <ActionButton id="mesh.add" />
          </>
        }
      >
        <div className="px-6">
          {s.members.length ? (
            s.members.map((m) => (
              <div className="row-item flex-wrap" key={m.id}>
                <div className="flex items-center gap-3">
                  <span className="surface-icon">
                    <Icon name="server" size={20} />
                  </span>
                  <div>
                    <div className="flex items-center gap-2 font-medium text-sm">
                      {m.id}
                      <Badge kind="default">{m.master ? "主机" : "从机"}</Badge>
                    </div>
                    <p className="text-xs text-muted-foreground mt-1">
                      {m.client?.server || m.host || "本机"} ·{" "}
                      {m.client ? "可作为客户端入口" : "尚未配置客户端入口"}
                    </p>
                  </div>
                </div>
                <div className="flex gap-2">
                  <ActionButton id="mesh.entry" context={{ id: m.id }}>
                    入口设置
                  </ActionButton>
                  {!m.master && (
                    <ActionMenu
                      compact
                      label={`管理服务器：${m.id}`}
                      items={[{ id: "mesh.remove", context: { id: m.id } }]}
                    />
                  )}
                </div>
              </div>
            ))
          ) : (
            <Empty
              title="尚未初始化主从管理"
              description="单机用户可直接使用本机出站。"
              icon="server"
            />
          )}
        </div>
      </Panel>
      <Panel
        title="主从线路"
        description="保存编排后统一应用，再到用户详情中分配。"
        actions={<ActionButton id="mesh.route" />}
      >
        {s.routes.length ? (
          <DataTable headings={["线路", "入口 / 路径 / 落地", "操作"]}>
            {s.routes.map((r) => (
              <TableRow key={r.id}>
                <TableCell className="font-medium">{r.id}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <Badge kind="default">{r.entry}</Badge>
                    <Icon name="next" className="text-muted-foreground" />
                    {r.hops.length > 0 && (
                      <>
                        <span>{r.hops.join(" → ")}</span>
                        <Icon name="next" className="text-muted-foreground" />
                      </>
                    )}
                    <Badge kind="default">{r.exit || "直接出站"}</Badge>
                  </div>
                </TableCell>
                <TableCell>
                  <div className="flex gap-1">
                    <ActionButton
                      id="mesh.route"
                      context={{
                        id: r.id,
                        entry: r.entry,
                        hops: r.hops.join(","),
                        exit: r.exit,
                      }}
                      variant="ghost"
                      size="sm"
                    >
                      编辑
                    </ActionButton>
                    <ActionMenu
                      compact
                      label={`管理线路：${r.id}`}
                      items={[
                        { id: "mesh.remove-route", context: { id: r.id } },
                      ]}
                    />
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </DataTable>
        ) : (
          <Empty title="尚无主从线路" icon="routes" />
        )}
      </Panel>
      <Panel
        title="本机出站与端点"
        description="第三方落地由独立服务提供，sbmgr 管理接入。"
        actions={<ActionButton id="proxy.add" />}
      >
        {s.proxies.length ? (
          <DataTable headings={["标识", "协议", "类别", "操作"]}>
            {s.proxies.map((p) => (
              <TableRow key={`${p.kind}:${p.tag}`}>
                <TableCell className="font-medium">{p.tag}</TableCell>
                <TableCell>
                  <Badge kind="default">{p.type}</Badge>
                </TableCell>
                <TableCell>{p.kind === "endpoint" ? "端点" : "出站"}</TableCell>
                <TableCell>
                  <div className="flex gap-1">
                    <ActionButton
                      id="proxy.replace"
                      context={{ kind: p.kind, tag: p.tag }}
                      variant="ghost"
                      size="sm"
                    >
                      替换
                    </ActionButton>
                    <ActionMenu
                      compact
                      label={`管理出站：${p.tag}`}
                      items={[
                        {
                          id: "proxy.delete",
                          context: { kind: p.kind, tag: p.tag },
                        },
                      ]}
                    />
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </DataTable>
        ) : (
          <Empty title="没有可管理的出站" icon="routes" />
        )}
      </Panel>
      <ActionButton id="mesh.recover" />
    </>
  );
}
