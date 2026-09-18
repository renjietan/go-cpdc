# CPDC Build BAT Design

## Goal

在 `scripts/build.bat`提供可双击的Windows构建入口，复用同目录现有 `build.ps1`生成全部CPDC发布文件。

## Behavior

- 使用 `%~dp0build.ps1`定位PowerShell脚本，不依赖启动时的当前工作目录。
- 使用Windows自带的 `powershell.exe`，仅为该进程指定 `ExecutionPolicy Bypass`，不修改系统策略。
- 完整保留 `build.ps1`的标准输出和错误输出。
- PowerShell构建失败时显示退出码、暂停窗口并以相同非零状态退出；成功时显示完成提示、暂停窗口并返回0。
- BAT不复制或重写PowerShell中的任何构建目标。

## Verification

从项目根目录以重定向按键的方式执行BAT，确认它生成五个目标并返回0；通过一个临时PATH遮蔽测试确认无法启动PowerShell时BAT返回非零状态。测试不修改构建产物之外的业务文件。
