import { ActionButton, Badge, PageHeader } from "../components/common";
import { DeviceDelivery } from "../components/DeviceDelivery";
import { useAppSelector } from "../store";
export function Subscriptions() {
  const s = useAppSelector((s) => s.admin.snapshot)!;

  return (
    <>
      <PageHeader
        title="订阅交付"
        description="每台设备独立授权，按需下载订阅。"
      />
      <div className="subscription-summary">
        <section className="setting-card">
          <div className="flex justify-between items-center">
            <h2>订阅服务</h2>
            <ActionButton id="subscription.set" variant="ghost" size="sm">
              编辑
            </ActionButton>
          </div>
          <div className="mt-4">
            <Badge kind={s.subscription.enabled ? "success" : "default"}>
              {s.subscription.enabled ? "已启用" : "未启用"}
            </Badge>
          </div>
          <p className="text-sm text-muted-foreground mt-3 break-all">
            {s.subscription.base_url || "尚未配置公开地址"}
          </p>
        </section>
        <section className="setting-card">
          <div className="flex justify-between items-center">
            <h2>客户端入口</h2>
            <ActionButton id="client.set" variant="ghost" size="sm">
              编辑
            </ActionButton>
          </div>
          <p className="font-medium text-sm mt-4 break-all">
            {s.client.server
              ? `${s.client.server}:${s.client.port}`
              : "尚未配置"}
          </p>
          <p className="text-xs text-muted-foreground mt-2">
            本机直出节点的连接地址
          </p>
        </section>
        <section className="setting-card">
          <div className="flex justify-between items-center">
            <h2>客户端模板</h2>
            <ActionButton id="template.set" variant="ghost" size="sm">
              编辑
            </ActionButton>
          </div>
          <p className="font-medium text-sm mt-4">
            {s.subscription.template ? "自定义 Mihomo 模板" : "简易配置"}
          </p>
          <p className="text-xs text-muted-foreground mt-2 break-all">
            {s.subscription.template_path || "使用默认规则与分组"}
          </p>
        </section>
      </div>
      <DeviceDelivery />
    </>
  );
}
