# CCU-Miniboard 构建设计

## 目标

为 Linux 2.6.32、32 位 ARMv7、VFPv3/NEON 的 CCU-Miniboard 生成可运行的静态 CPDC 可执行文件。该目标必须使用仓库内提供的 Go 1.23.12，且不能改变其他发布目标所使用的 Go 工具链。

## 构建入口

新增独立 PowerShell 脚本 `scripts/build-miniboard.ps1`。现有 `scripts/build.ps1` 保持不变，因此 Windows、CCU、MultibandRadio、MultibandHandheld 和 HF 仍由调用者当前环境中的系统 Go 编译。

MiniBoard 脚本使用固定输入 `compiler/go1.23.12.windows-amd64.zip`。如果专用工具链尚未展开，脚本将其解压到 `compiler/.go1.23.12/`；点号目录可避免 `go test ./...` 遍历 Go 工具链自身的源码。如果目录已经存在，则直接验证并复用。脚本不删除工具链目录，也不修改系统 `PATH`。

## 兼容性调整

主模块的 Go 版本调整为 1.23，并把目前要求 Go 1.25 的 `golang.org/x/sys` 降到支持 Go 1.23 且覆盖项目现有 API 用法的版本。`google.golang.org/protobuf` 保持当前支持 Go 1.23 的版本。

这些模块约束对所有构建都生效，但不会强制其他目标使用 Go 1.23；较新的系统 Go 仍能正常构建该模块。

## 编译参数与产物

脚本只在自身进程范围内设置以下环境变量，并在结束时恢复原值：

- `GOOS=linux`
- `GOARCH=arm`
- `GOARM=7`
- `CGO_ENABLED=0`
- `GOTOOLCHAIN=local`

编译继续使用 `-trimpath` 和 `-ldflags "-s -w"`。产物路径为 `dist/CPDC-CCU-Miniboard`。

该目标不传入 `Target=ccu`，使用默认的 `generic` 构建标记，因此空间预检倍率为 1。

## 错误处理

脚本在以下情况立即失败并给出明确错误：

- 编译器 ZIP 不存在；
- 已展开工具链缺少 `go.exe`；
- 工具链版本不是 Go 1.23.12；
- 依赖解析、测试或编译失败。

脚本不会自动清理不完整目录，以符合项目禁止批量删除文件或目录的规则。

## 验证

实现使用自动化脚本测试覆盖工具链选择、目标参数、输出名称和不影响现有构建入口等约束。随后使用 Go 1.23.12 执行 `go test ./...` 和实际交叉编译，并通过 Go 工具读取产物元数据，确认其为 `linux/arm`、非 CGO 构建且使用 Go 1.23.12。
