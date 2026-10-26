# 发布流程

sbmgr 的版本来自 Git tag，由 `deploy/build-linux.sh` 注入二进制；应用内不维护版本历史、不下载更新、不回滚软件版本。业务状态、基础模板、运行配置和失败恢复仍由运维流程负责。

## 版本与标签

- 使用语义化版本 `vX.Y.Z`，标签必须指向 `main` 上的提交。
- 标签格式错误或不在 `main` 上时，Release 工作流会拒绝发布。
- 不在同一标签下重建或覆盖产物；需要修正时发布新的补丁版本。
- `sbmgr version` 只读构建信息，不能修改版本。

## 发布前

1. 确认 `main` 的 CI、CodeQL 全绿，Dependabot 安全更新已处理。
2. 更新 [CHANGELOG](../CHANGELOG.md)：把 `Unreleased` 内容归入新版本，说明状态/schema 迁移、兼容性和失败恢复。
3. 检查文档是否覆盖新行为；涉及配置事务或部署步骤时同步[运维指南](OPERATIONS.md)。
4. 本地运行 `make release-check` 或 `sh scripts/verify.sh`；Linux 发布构建要求干净工作树和精确标签，构建前不要提交生成物。

## 发布步骤

```sh
# 1. 提交变更日志，保持单一目的
git add CHANGELOG.md
git commit -m "chore(release): vX.Y.Z"

# 2. 在 main 上创建签名或附注标签
git tag -a vX.Y.Z -m "sbmgr vX.Y.Z"

# 3. 推送提交和标签
git push origin main
git push origin vX.Y.Z
```

推送 `v*` 标签会触发 `.github/workflows/release.yml`：

1. 校验标签格式和 `main` 祖先关系。
2. 重跑 Go 格式、模块整洁、vet、测试、公开树检查、部署脚本测试和前端验证。
3. 调用 `deploy/build-linux.sh`，从标签和 HEAD 注入版本与 commit。
4. 对 `.dist/sbmgr-X.Y.Z-linux-amd64` 生成构建来源证明（attestation）。
5. 使用 `gh release create --verify-tag --generate-notes` 创建 GitHub Release，附加二进制和 `.sha256` 校验文件。

发布说明由 GitHub 按 `.github/release.yml` 分类生成；需要人工补充时在发布后编辑 Release，不要重新推送标签。

## 产物与校验

发布产物：

```text
.dist/sbmgr-X.Y.Z-linux-amd64
.dist/sbmgr-X.Y.Z-linux-amd64.sha256
```

下载后校验：

```sh
sha256sum -c sbmgr-X.Y.Z-linux-amd64.sha256
./sbmgr-X.Y.Z-linux-amd64 version
gh attestation verify sbmgr-X.Y.Z-linux-amd64 --repo buerka/Sbmgr
```

部署方仍须按[运维指南](OPERATIONS.md)核对来源、tag、commit、SHA256、systemd 单元和实际代理请求；构建来源证明不能替代目标环境验收。

## 失败与恢复

- 工作流在创建 Release 前失败：修复原因后重跑同一工作流；不要移动已推送的标签。
- Release 已创建但资产缺失：使用 `gh release upload vX.Y.Z <file> --clobber` 补齐，或删除该 Release 后重跑（仅维护者）。
- 标签指向错误提交或产物有缺陷：保留历史标签，发布新的 `vX.Y.(Z+1)` 修正版本；不要删除已公开的发布历史。
- 部署回滚遵循外部部署事务：恢复上一版业务状态与配置，旧二进制仅在事务内临时保留；应用内不支持软件回滚。

## 仓库设置建议

维护者应在 GitHub 上保护 `main`：要求 CI 的 `policy`、`frontend`、`go-test`、`privilege`、`go-test-macos`、`go-compile-windows`、`build` 通过，禁止强制推送和直接删除分支。标签建议开启保护规则，只允许维护者创建 `v*` 标签。
