# 内嵌前端构建目录

前端源码在 `/frontend`。执行 `npm --prefix frontend ci` 和 `npm --prefix frontend run build` 后，Vite 将静态资源写入此目录下的 `dist/`，由 Go embed 编入二进制。

构建资源不进入 Git。本文件确保纯 Go 包可在尚未安装前端依赖的源码树中编译；完整 Web 预览和发布必须先构建前端。
