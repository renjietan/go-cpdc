# CPDC Windows Build Design

## Goal

在现有四个Linux发布文件之外，从同一套CPDC源码生成Windows amd64发布文件 `CPDC.exe`。

## Build contract

- `CPDC.exe`: `GOOS=windows`、`GOARCH=amd64`、`CGO_ENABLED=0`。
- Windows版本不设置 `GOARM`或 `GOARM64`，也不设置CCU专用构建目标，因此使用普通的1倍工作空间预检。
- 现有四个Linux发布文件、文件名和构建参数保持不变。

## Implementation

扩展 `scripts/build.ps1`中的共用 `Build-CPDC`函数，使 `GOOS`成为带默认值 `linux`的参数；新增一次 `CPDC.exe`的Windows amd64调用。继续由函数保存和恢复全部Go环境变量，输出仍统一写入 `dist/`。

同步更新CPDC需求文档和README中的发布目标与构建产物数量。Proto、运行时状态机和网络协议不变。

## Verification

执行完整Go测试和静态检查，再运行 `scripts/build.ps1`。使用 `go version -m dist/CPDC.exe`确认Windows二进制为 `GOOS=windows`、`GOARCH=amd64`、`CGO_ENABLED=0`，并确认原有四个Linux文件仍然生成。
