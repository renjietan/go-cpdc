# CPDC Implementation Plan

> 执行说明：用户已明确授权技术方案完成后自动实施，无需等待确认。本目录不是 Git 仓库，因此计划中的检查点以测试结果替代提交记录。

**Goal:** 实现可交叉编译的纯 Go CPDC，完整覆盖发现、认证、可靠广播文件接收、安全解析、设备类型产物和结果确认。

**Architecture:** 使用串行客户端状态机管理协议会话，UDP 传输层只负责收发，ZIP 与产物包只负责确定性文件处理。protobuf 为唯一业务载荷格式，外层固定 4 字节 magic。

**Tech Stack:** Go 1.25、proto3、`google.golang.org/protobuf`、Go 标准库、必要的 `golang.org/x/sys`。

---

## Task 1: 工程骨架与 protobuf

**Files:**
- Create: `go.mod`
- Create: `buf.yaml`
- Create: `buf.gen.yaml`
- Create: `scripts/generate-proto.ps1`
- Create: `gen/cpd/v1/cpd.pb.go`（生成）

1. 建立 Go module 并固定直接依赖。
2. 安装项目本地生成工具并由现有 `proto/cpd.proto` 生成 Go 类型。
3. 运行 `go test ./...` 和 `go vet ./...` 验证生成代码。

## Task 2: 配置、ESN 与文件基础设施

**Files:**
- Test: `internal/config/config_test.go`
- Create: `internal/config/config.go`
- Create: `internal/config/esn.go`
- Test: `internal/fsutil/files_test.go`
- Create: `internal/fsutil/files.go`
- Create: `internal/fsutil/space_unix.go`
- Create: `internal/fsutil/space_windows.go`
- Create: `internal/buildinfo/buildinfo.go`

1. 先写配置组合、未知类型、ESN 格式和快照更新失败测试并确认失败。
2. 实现配置读取、设备类型校验、固定 39 位 ESN 和原子 JSON 写回。
3. 先写原子替换、直接文件清理和空间计算测试并确认失败。
4. 实现文件工具和跨平台可用空间查询；逐项运行测试。

## Task 3: 协议编解码与网络层

**Files:**
- Test: `internal/protocol/codec_test.go`
- Create: `internal/protocol/codec.go`
- Create: `internal/protocol/id.go`
- Create: `internal/protocol/timing.go`
- Test: `internal/netio/interface_test.go`
- Create: `internal/netio/interface.go`
- Create: `internal/netio/udp.go`

1. 先写 magic、最大长度、非法 body、UUID 和消息往返测试并确认失败。
2. 实现 protobuf envelope 外壳与严格字段校验。
3. 先写网卡唯一选择、显式选择和子网掩码测试并确认失败。
4. 实现候选网卡选择、39001 接收以及广播/回环双发。

## Task 4: 认证与传输核心

**Files:**
- Test: `internal/client/auth_test.go`
- Create: `internal/client/auth.go`
- Test: `internal/client/transfer_test.go`
- Create: `internal/client/transfer.go`
- Create: `internal/client/session.go`

1. 写认证分片聚合、冲突、缺失和新会话抢占测试并确认失败。
2. 实现按 ESN/类型匹配的认证聚合器。
3. 写 chunk 边界、CRC、重复包、位图、缺包区间拆分和 SHA-256 测试并确认失败。
4. 实现单临时文件 `WriteAt` 接收器、缓存命中和完成校验。

## Task 5: ZIP 安全校验与业务索引

**Files:**
- Test: `internal/packagezip/validate_test.go`
- Test: `internal/packagezip/business_test.go`
- Create: `internal/packagezip/limits.go`
- Create: `internal/packagezip/validate.go`
- Create: `internal/packagezip/business.go`

1. 构造内存 ZIP，写路径穿越、重复路径、压缩比、特殊文件、条目/大小边界测试并确认失败。
2. 实现中央目录预扫描和流式实际写入校验。
3. 写目录结构、JSON、Guid 宽容类型、节点设备引用、Radio Subnet 和字段限制测试并确认失败。
4. 实现业务索引，使输出层不重复解析原始 JSON。

## Task 6: 各设备类型产物

**Files:**
- Test: `internal/output/radio_test.go`
- Test: `internal/output/ccu_test.go`
- Test: `internal/output/server_test.go`
- Create: `internal/output/output.go`
- Create: `internal/output/radio.go`
- Create: `internal/output/ini.go`
- Create: `internal/output/ccu.go`
- Create: `internal/output/server.go`

1. 写 plan、mission、INI 内容测试并确认失败。
2. 实现确定性 tar 和 INI 保留式更新。
3. 写 CCU 清理/复制和 Server/IEC 根级 local_node 测试并确认失败。
4. 实现临时产物校验后按配置顺序提交，明确 skipped 结果。

## Task 7: 完整客户端状态机

**Files:**
- Test: `internal/client/client_test.go`
- Create: `internal/client/client.go`
- Create: `internal/client/messages.go`
- Create: `internal/client/parser.go`
- Create: `internal/client/result.go`

1. 写发现、AUTH 窗口、缓存命中仍解析、传输进度、解析 30 秒超时、完成重试五次、终态忽略和 DISCOVER 重置测试并确认失败。
2. 实现串行事件循环、定时器与可取消解析任务。
3. 验证错误全部映射为 proto 枚举，不向协议或 UI 暴露任意错误字符串。

## Task 8: 入口、构建与集成验证

**Files:**
- Create: `cmd/cpdc/main.go`
- Create: `scripts/build.ps1`
- Test: `internal/client/integration_test.go`
- Create: `README.md`

1. 实现 `--interface`、可执行目录解析、结构化日志和优雅退出。
2. 添加本机回环最小集成测试。
3. 运行 `gofmt`、`go test ./...`、`go test -race ./...`（当前平台）、`go vet ./...`。
4. 交叉编译 CPDC-CCU、CPDC-MultibandRadio、CPDC-MultibandHandheld 和 CPDC-HF。
5. 复核需求中的消息名、端口、窗口、路径、限制和错误枚举均有实现或测试证据。
