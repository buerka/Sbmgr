import * as Tabs from "@radix-ui/react-tabs";
import { ActionButton, PageHeader } from "../components/common";
import { Alert } from "../components/ui/feedback";
import { useAppSelector } from "../store";
import { RouteResources } from "./RouteResources";
import { RouteCanvas } from "../components/RouteCanvas";
export function RoutesPage() {
  const s = useAppSelector((s) => s.admin.snapshot)!;
  return (
    <>
      <PageHeader
        title="线路管理"
        description="连接入口与落地，保存线路后分配给用户。"
        actions={
          <>
            <ActionButton id="mesh.check" />
            <ActionButton id="mesh.apply" variant="default" />
          </>
        }
      />
      {s.mesh_pending && (
        <Alert kind="warning">
          有线路修改尚未应用。点击「应用拓扑」使连线生效，再为用户分配新线路。
        </Alert>
      )}
      <Tabs.Root defaultValue="lines">
        <Tabs.List className="tab-list" aria-label="线路管理分类">
          <Tabs.Trigger value="lines">线路画布</Tabs.Trigger>
          <Tabs.Trigger value="resources">服务器与落地</Tabs.Trigger>
        </Tabs.List>
        <Tabs.Content
          value="lines"
          forceMount
          className="tab-content data-[state=inactive]:hidden"
        >
          <RouteCanvas />
        </Tabs.Content>
        <Tabs.Content value="resources" className="tab-content">
          <RouteResources />
        </Tabs.Content>
      </Tabs.Root>
    </>
  );
}
