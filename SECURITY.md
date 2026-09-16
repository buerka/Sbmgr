# 安全策略

未修复漏洞通过仓库 **Security → Report a vulnerability** 私密报告，提供版本、影响、最小复现与缓解方式。公开 issue 不放漏洞利用细节或真实凭据、配置、访问记录。

敏感值若进入 Git 历史，立即轮换对应凭据，再按影响处理历史；仅删除当前文件不能撤销暴露。

提交边界见 [AGENTS.md](AGENTS.md)，源码修复追踪见[审计记录](docs/SECURITY-REMEDIATION-20260905.md)。安全更新按[外部部署流程](docs/OPERATIONS.md)核验来源、tag、commit 与 SHA256。
