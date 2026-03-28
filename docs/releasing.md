# 发布说明

本文档说明如何从当前源码仓库生成发布包，并发布到 GitHub Releases。

## 目标

正式发布流程为：

1. 在本地源码仓库编译与打包
2. 生成 `openclaw-guard-kit-windows-x64.zip`
3. 创建 Git tag
4. 发布到 GitHub Releases
5. 用户通过 `installer/install.ps1` 和已安装目录中的 `installer/update.ps1` 完成安装与升级

## 本地构建

在仓库根目录执行：

```powershell
go build ./...
powershell -ExecutionPolicy Bypass -File .\packaging\package.ps1 -Version v0.1.0
```

打包完成后，发布包位于：

```
dist\openclaw-guard-kit-windows-x64.zip
```

## 发布 Git tag

```powershell
git tag v0.1.0
git push origin v0.1.0
```

## 创建 GitHub Release

如果本机已安装 GitHub CLI，可执行：

```powershell
gh release create v0.1.0 .\dist\openclaw-guard-kit-windows-x64.zip --title "v0.1.0" --notes "Official release."
```

如果不使用 GitHub CLI，也可以手动在 GitHub 网页中创建 Release，并上传：

```
dist\openclaw-guard-kit-windows-x64.zip
```

## 安装与升级入口

| 操作 | 入口脚本 |
|------|---------|
| 首次安装 | `installer/install.ps1` |
| 已安装后的升级 | 安装目录中的 `installer/update.ps1` |

这两个入口脚本都会从 GitHub Releases 下载最新发布包，再分别调用：

- `installer/install-package.ps1`
- `installer/update-from-dir.ps1`

## 注意事项

- 源码仓库不要提交 exe、zip、dist 等发布产物
- 正式用户安装与升级应始终以 GitHub Releases 为唯一发布源
- `install-package.ps1` 和 `update-from-dir.ps1` 当前仍兼容旧安装逻辑，因此发布包中保留了部分完整性校验所需文件
