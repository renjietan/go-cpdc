# HF Go 1.23.12 构建设计

## 目标

HF 短波发布文件必须与 CCUAudio 一样，使用仓库内 `build/compiler/go1.23.12.windows-amd64.zip` 提供的 Go 1.23.12 工具链编译。产物仍为 `build/dist/bin/CPDC-HF`。

## 构建入口

- `build/scripts/build.ps1` 不再生成 `CPDC-HF`，避免系统 Go 工具链产生不符合要求的 HF 产物。
- `build/scripts/build-audio.ps1` 在完成工具链解压、版本校验和 Go 测试后，使用同一工具链依次生成 `CPDC-CCU-Audio` 与 `CPDC-HF`。
- 两个目标均使用 `GOOS=linux`、`GOARCH=arm`、`GOARM=7`、`CGO_ENABLED=0`，且均不启用 CCU 的双倍工作空间链接标记。
- 任一目标编译失败时，脚本以失败状态结束并报告对应目标。

## 验证

扩展 `tests/build_audio.test.ps1`，执行专用构建脚本后验证两个产物均存在，并通过 Go 1.23.12 的 `version -m` 检查以下元数据：

- `go1.23.12`
- `GOOS=linux`
- `GOARCH=arm`
- `GOARM=7`
- `CGO_ENABLED=0`

测试还要确认两个产物均未包含 `cpdc/internal/buildinfo.Target=ccu`。另对统一脚本做静态断言，确保它不再生成 `CPDC-HF`。

## 文档同步

更新 CPDC 的构建目标需求，说明 HF 使用仓库内 Go 1.23.12 专用工具链并由专用脚本生成。依据项目约束，将 `docs/requirements` 中修改的对应文档同步到 `D:\CPD\CPDS`，确保两边一致。
