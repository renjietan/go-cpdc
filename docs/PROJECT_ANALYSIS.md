# CPDC 项目研读报告（PROJECT_ANALYSIS）

> 本报告依据《研读项目提示词》对项目进行穷尽式源码研读。所有结论均基于实际读取的源码、配置、需求与测试文件，并标注「文件:行号」作为证据；无法确认处标注「待确认」。

## 0. 项目基本信息

- 项目名称：CPDC（Communication Plan Distribution Client，通信预案分发客户端）
- 源码根目录：`D:\work\flutter\template\3-38-10\CPDC-main`（注意：目录名含 `flutter`，但实际是纯 Go 项目，与 Flutter 无关）
- 一句话定位：部署在 CCU/MultiBand/HF/车辆接口等嵌入式环境的配置包接收与解析程序，通过 IPv4 UDP 广播接收 CPDS 下发的 ZIP 通信保障配置包，校验后按设备类型解析生成正式输出并原子更新成功快照。
- 文档状态：需求基线草案（需求文档日期 2026-07-20）

---

## 1. 技术栈与语言版本清单（精确）

### 1.1 编程语言

| 语言 | 精确版本/声明 | 版本证据 | 职责 |
|---|---|---|---|
| Go | `go 1.23.0`（go.mod 声明的模块语言版本） | `go.mod:1-3` | 全部业务逻辑、网络、解析、输出、构建 |
| Go（专用工具链） | `go1.23.12`（仓库内自带的交叉编译工具链） | `build/compiler/go1.23.12.windows-amd64.zip`、`build/scripts/build-audio.ps1:7` | 编译 `CPDC-HF`、`CPDC-CCU-Audio`（Linux ARMv7） |

### 1.2 依赖（来自 go.mod / go.sum）

| 模块 | 版本 | 用途 | 证据 |
|---|---|---|---|
| `golang.org/x/sys` | `v0.30.0` | Windows/Linux 系统调用：广播 socket 选项、磁盘剩余空间、`MoveFileEx`、`Statfs`、`unix.Sync` | `go.mod:5-8`、`internal/netio/broadcast_windows.go`、`internal/fsutil/space_windows.go` |
| `google.golang.org/protobuf` | `v1.36.11` | Protobuf 编解码（`proto.Marshal/Unmarshal/Size/Clone`） | `go.mod:9`、`internal/protocol/codec.go` |
| `github.com/google/go-cmp` | `v0.7.0` | 测试断言（间接依赖，仅测试用） | `go.sum:1-2` |

### 1.3 构建/代码生成工具

| 工具 | 版本 | 用途 | 证据 |
|---|---|---|---|
| `buf` | `v1.72.0` | 生成 protobuf Go 代码 | `build/scripts/generate-proto.ps1:2` |
| `protoc-gen-go` | `v1.36.11` | buf 的 Go 代码生成插件 | `build/scripts/generate-proto.ps1:3` |

### 1.4 发布目标

| 发布文件 | GOOS | GOARCH | 附加参数 | 工作空间因子 | 证据 |
|---|---|---|---|---|---|
| `CPDC.exe` | windows | amd64 | 无 | 1× | `build/scripts/build.ps1` |
| `CPDC-CCU` | linux | amd64 | `-X buildinfo.Target=ccu` | 2× | `build/scripts/build.ps1`、`internal/buildinfo/buildinfo.go:6-9` |
| `CPDC-MultibandRadio` | linux | arm64 | `GOARM64=v8.0` | 1× | `build/scripts/build.ps1` |
| `CPDC-MultibandHandheld` | linux | arm | `GOARM=7` | 1× | `build/scripts/build.ps1` |
| `CPDC-VehInte` | linux | arm64 | 无 | 1× | `build/scripts/build.ps1`（注意二进制名拼写为 `CPDC-VehInte`） |
| `CPDC-CCU-Audio` | linux | arm | `GOARM=7`，仓库内 go1.23.12 | 1× | `build/scripts/build-audio.ps1` |
| `CPDC-HF` | linux | arm | `GOARM=7`，仓库内 go1.23.12 | 1× | `build/scripts/build-audio.ps1` |

所有产物 `CGO_ENABLED=0`，并使用 `-trimpath -ldflags "-s -w"` 剥离调试信息与符号。

---

## 2. 项目概览与功能全景图

目标用户：嵌入式设备（CCU、MultiBand 电台、HF 电台、车辆接口终端）的运维/部署人员。

使用场景：CPDS（服务端）在二层广播域内向本机及其他设备的 CPDC 实例下发「通信保障配置包」，CPDC 自动完成发现、认证、接收、校验、解析、写盘与结果回执，无需人工交互。

### 功能清单索引

1. 启动与命令行参数解析
2. 配置加载与组合校验
3. ESN 生成与持久化
4. 网络接口发现与选择
5. UDP 传输层（监听 + 广播/环回双发 + 失败刷新）
6. Linux rp_filter 控制
7. 协议编解码（Magic 前缀 + protobuf 信封）
8. UUID v4 生成与校验
9. 时间参数集中定义
10. 客户端状态机
11. 发现（DISCOVER_NTY / DISCOVER_RSP）
12. 认证（AUTH_NTY / AUTH_RSP）
13. 文件传输（START/CHUNK/END/LOSSPACK/PROGRESS/COMPLETE）
14. 原始包缓存复用
15. 缺包请求分包
16. 解析流程编排
17. 通信包 ZIP 结构校验
18. 通信包业务结构校验与设备解析
19. 输出生成器分发
20. 无线电（Radio）输出
21. CCU / CCUAudio 输出
22. Server / IEC 输出
23. VehInter 输出
24. INI 结构化更新
25. 成功快照持久化
26. 解析结果上报 / 重试 / ACK
27. 会话恢复与新会话抢占
28. 日志与脱敏
29. 构建与发布
30. 文件系统原子操作工具

---

## 3. 目录结构解析

```
cmd/cpdc/main.go          程序入口：命令行、日志、启动、网络初始化、事件循环
internal/buildinfo/       构建信息：Target（generic/ccu）、WorkspaceFactor
internal/client/          客户端状态机与协议处理（核心）
internal/config/          配置加载、ESN 生成、快照持久化
internal/fsutil/          原子写/拷贝、目录清理、空间计算、Sync、跨平台替换
internal/netio/           UDP 传输、接口枚举、rp_filter 控制
internal/output/          按设备类型生成正式输出（radio/ccu/server/vehinter/ini）
internal/packagezip/      ZIP 结构校验、业务结构校验、设备/节点解析
internal/protocol/        Magic+protobuf 编解码、UUID、时间常量
proto/cpd.proto           Protobuf 协议基线（12 类消息、9 种设备类型、24 个错误码）
gen/cpd/v1/cpd.pb.go      buf 生成的 Go 代码
docs/requirements/        三份需求说明书（01 CPDS、02 CPDC、03 交互流程）
docs/superpowers/         设计文档与实施计划（plans/specs）
docs/examples/            配置样例
build/scripts/            构建/代码生成脚本
build/compiler/           仓库内 Go 1.23.12 工具链
tests/                    PowerShell 构建/音频构建验收脚本
```

核心入口：`cmd/cpdc/main.go:31`（`run()`）；协议入口：`internal/client/run.go:15`（`Run`）；状态机入口：`internal/client/client.go:140`（`Handle`）与 `:171`（`Tick`）。

---

## 4. 架构说明

### 4.1 分层结构

- 网络层 `netio`：封装 UDP 监听、多接口广播发送、环回发送、失败重枚举与 rp_filter 控制。
- 协议层 `protocol`：Magic 前缀 + protobuf 二进制信封编解码、UUID v4、时间常量。
- 状态机层 `client`：`Client` 维护会话状态并处理 12 类消息，`Transfer` 负责文件分块落盘，`Parser` 负责解析编排。
- 校验层 `packagezip`：ZIP 结构与业务结构双重校验，产出 `Package`（含 `Nodes`/`Devices`）。
- 输出层 `output`：按设备类型把 `Package` 转换成正式产物。
- 持久层 `config` + `fsutil`：配置/快照原子写、缓存落盘、空间计算。

### 4.2 数据流（一次完整下发）

1. `main.run` 读取并校验配置、生成/复用 ESN → `cmd/cpdc/main.go:31-95`
2. `netio.SelectCandidates` 枚举合格广播接口 → `netio.Listen` 绑定 `0.0.0.0:39001` 并建立发送池 → `internal/netio/udp.go:62-88`
3. `client.Run` 事件循环：`transport.Read` 读包 → `protocol.Unmarshal` → `client.Handle` 返回回复 → `transport.Send` 广播+环回 → `client.Tick` 处理超时/重试 → `internal/client/run.go:15-48`
4. 认证完成进入 `StateAuthenticated`，收到 `TRANSFER_START_NTY` 后预检并创建接收文件 → `internal/client/client.go:360-439`
5. 分块写入、END 后校验 SHA-256 并提交缓存 → `internal/client/transfer.go`
6. `beginParse` 启动协程，`packagezip.Validate` + `output.Process` 生成产物 → `internal/client/parser.go:66-127`
7. 成功后 `config.UpdateSnapshot` 原子更新快照，再发 `PARSE_COMPLETE_REQ` 并最多重试 5 次 → `internal/client/client.go:538-560`

### 4.3 状态机

状态定义见 `internal/client/client.go:21-29`：

`idle → discovered → authenticating → authenticated → receiving → parsing → reporting → terminal`

- `terminal` 后只接受新 `session_id` 的 `DISCOVER_NTY`（`Handle:147-153`）。
- `reporting` 阶段仅额外接受匹配的 `PARSE_COMPLETE_ACK`（`Handle:147-152`）。
- 解析期间新会话认证触发协作式取消/抢占（`queueTakeover` / `activateTakeover`，`client.go:654-690`）。

---

## 5. 运行、构建、部署

### 5.1 命令行参数（`cmd/cpdc/main.go:32-35`）

| 参数 | 默认 | 说明 |
|---|---|---|
| `--interface` | 空 | 限制为单一广播网卡（诊断用）；空则使用全部合格接口 |
| `--log-level` | `info` | `debug/info/warn/error` |
| `--config-path` | 空 | `cpdc_config.json` 的路径（文件或目录）；空则用可执行文件同目录 |

### 5.2 开发验证

```powershell
go test ./...
go vet ./...
```

### 5.3 构建

```powershell
.\build\scripts\generate-proto.ps1   # 重新生成 protobuf
.\build\scripts\build.ps1            # 5 个产物（含 VehInte）
.\build\scripts\build-audio.ps1      # CPDC-CCU-Audio + CPDC-HF（go1.23.12）
```

Windows 下也可双击 `build\scripts\build.bat`（内部依次调用 build.ps1 与 build-audio.ps1）。

### 5.4 部署

发布文件、`cpdc_config.json`、设备已有 `ReReadJson.ini` 放同一目录；首次启动 `esn` 留空以自动生成并持久化 39 位 ESN；`--config-path` 可指定配置路径；`ReReadjsonPath` 字段指定要更新的 INI 文件路径（`README.md`、`cpdc_config.json`）。

---

## 6. 功能点穷尽式拆解

### 6.1 启动与命令行参数解析

- 入口 `main()` → `run()`：`cmd/cpdc/main.go:24-31`。
- 参数解析 `flag.String`：`main.go:32-35`。
- 日志级别映射 `parseLogLevel`：`main.go:100-111`。
- 使用 `os.Executable()` 取可执行文件目录作为根，相对输出路径以该目录为基准，不使用进程 cwd：`main.go:44-48`。
- 若配置含 `cachePath`，则以它作为数据根 `dataRoot` 并创建：`main.go:54-59`。
- 启动时对 `dataRoot`、`txbz`、`txbz_ccu`、`txbz_server` 逐个清理遗留临时文件：`main.go:61-64`（配合 `fsutil.CleanupKnownTemps`）。
- 生成 16 字节 `instance_nonce`（仅用于发现回复）：`main.go:78-80`。
- 信号处理：`os.Interrupt`、`SIGTERM` → 优雅退出并 `transport.Close()` 恢复 rp_filter：`main.go:95-97`、`netio/udp.go:279-293`。

### 6.2 配置加载与组合校验

- 常量 `FileName = "cpdc_config.json"`：`internal/config/config.go:19`。
- `LoadPath`：空路径用可执行目录；指向目录则拼接固定文件名：`config.go:49-59`。
- `load`：读文件 → 解析原始 `map[string]any`（保留未知字段）+ 解析类型化 `disk`：`config.go:65-85`。
- 字段：`deviceTypes/esn/txbzName/txbzHash/nodeId/deviceIds/ReReadjsonPath/tarPath/cachePath`：`config.go:23-36`、`:72-83`。
- `txbzHash` 非空时必须是 64 位 SHA-256 十六进制：`config.go:109-113`。
- `parseTypes` 组合规则：`config.go:185-227`：
  - 空数组报错；未知类型报错；重复类型报错；
  - `Server` 与 `IEC` 互斥；无线电类型（HF/MultiBandRadio/MultibandHandheld/SmallHandheld）至多一种；`CCUAudio` 必须独占。
- 类型名与枚举互转：`DeviceTypeByName` / `DeviceTypeName`：`config.go:229-256`。

### 6.3 ESN 生成与持久化

- `GenerateESN`：从 `crypto/rand.Reader` 读 16 字节（128 位）→ 大整数转十进制 → 不足 39 位左侧补零 → 全零重试：`internal/config/esn.go:11-33`。
- 校验：`esnPattern = ^[0-9]{39}$` 且不能全零：`config.go:21`、`:103`。
- 首次生成后立即原子写回并持久化：`config.go:92-104`、`:131-135`。
- 从不因网络消息自动改号；人工清空 `esn` 后重启才重新生成（需求 02 §3.4）。

### 6.4 网络接口发现与选择

- `Candidate`：接口名/索引/IP/掩码：`internal/netio/interface.go:11-16`。
- 合格接口：已启用 + 支持广播 + 非回环 + 非点对点；每个接口取非回环 IPv4 中数值最小者：`interface.go:51-80`。
- 稳定排序：按索引、接口名、IPv4：`interface.go:111-122`。
- `--interface` 精确匹配唯一接口：`interface.go:83-101`。
- `DISCOVER_RSP` 的 `current_ip/subnet_mask` 取稳定排序后第一张候选接口：`cmd/cpdc/main.go:82-83`。

### 6.5 UDP 传输层

- 端口常量：接收 `39001`、回复 `39002`：`internal/netio/udp.go:13-16`。
- 广播目标 `255.255.255.255:39002`、环回目标 `127.0.0.1:39002`：`udp.go:18-20`。
- `Listen`：绑定 `0.0.0.0:39001`，设 4 MiB 读缓冲，先 `rp_filter.Apply` 再建发送池：`udp.go:62-88`。
- 每张候选接口建立独立发送套接字（绑定接口 IP，端口 0），启用广播、设 1 MiB 写缓冲：`udp.go:122-137`、`openBroadcastSender`。
- `Send`：每张接口各发一份广播 + 恰好一份环回；任一路广播失败 → 重新枚举并整池替换 → 用原报文仅重试一次物理广播（不重发环回）→ 重试仍失败的接口从活动池淘汰：`udp.go:152-188`。
- `writeCopies` 统计每目标成败，短写视为失败：`udp.go:248-277`。
- 收到报文不转发、不反射（设计上杜绝广播风暴）。

### 6.6 Linux rp_filter 控制

- `rpFilterController` 接口 + `fileRPFilterStore`（读写 `/proc/sys/net/ipv4/conf/<if>/rp_filter`）：`internal/netio/rpfilter.go:12-52`。
- 禁用时始终先记录原值、再写 `0`，并额外处理 `all`：`rpfilter.go:69-107`、`rpFilterNames:160-172`。
- 失败回滚本次新增修改；`Restore` 逆序恢复并幂等：`rpfilter.go:109-158`。
- Linux 实现：`rpfilter_linux.go`（根 `/proc/sys/net/ipv4/conf`）；非 Linux 为 no-op：`rpfilter_other.go`。

### 6.7 协议编解码

- 常量：`Magic = 0xEEDDCCBB`（大端线序 `EE DD CC BB`）、`MagicBytes=4`、`MaxPacketBytes=1396`、`MaxDatagramBytes=1400`：`internal/protocol/codec.go:12-16`。
- `Marshal`：先校验信封 → protobuf 确定性序列化 → 长度校验 → 前置 Magic：`codec.go:24-42`。
- `Unmarshal`：长度校验 → Magic 校验（不匹配返回 `ErrForeignPacket`，不反序列化）→ protobuf 解码 → 信封校验：`codec.go:44-59`。
- `ValidateEnvelope`：`session_id`、`message_id` 必须是 UUID v4，`body` 非空：`codec.go:61-73`。
- 协议消息定义：`proto/cpd.proto`（12 类消息、`Result`/`DeviceType`/`ErrorCode`/`TransferStage`/`ParseStage` 枚举）。

### 6.8 UUID v4 生成与校验

- `NewID`：16 字节随机 + 设置版本位 `0x40`、变体位 `0x80`：`internal/protocol/id.go:13-21`。
- `ValidID`：长度 16 且版本=4、变体正确：`id.go:23-25`。
- `SameID`/`CloneID`：`id.go:27-32`。

### 6.9 时间参数集中定义

全部生产时间常量集中在 `internal/protocol/timing.go:6-18`：`DiscoverInterval=1s`、`DiscoverWindow=5s`、`AuthInterval=1s`、`AuthWindow=5s`、`RecentDiscoverValidity=10s`、`TransferSilenceTimeout=10s`、`ClientSessionIdle=30s`、`ParseTimeout=30s`、`ParseResultRetry=1s`、`ParseResultMaxAttempts=5`、`EventLoopPoll=100ms`。

### 6.10 客户端状态机（`Client`）

- 状态定义：`client.go:19-29`；结构体字段（会话、认证、传输、解析、上报、抢占）：`client.go:59-102`。
- `New`：校验选项，默认注入 `fsutil.AvailableSpace`、`protocol.NewRandomID`、`NewParser().Parse`；`tarPath` 非空时作为工作空间并创建：`client.go:104-134`。
- `Handle`：统一入口，先校验 ID/body，再按消息类型分派：`client.go:140-169`。
- `Tick`：清理过期发现记录、消费解析事件、处理认证超时/会话静默/解析截止/结果重试：`client.go:171-236`。

### 6.11 发现

- `handleDiscover`：终态/reporting 且同会话时忽略；否则重置旧会话并记录近期发现，Idle→Discovered，返回 `DiscoverRsp`（esn/instance_nonce/device_types/current_ip/subnet_mask）：`client.go:238-257`。
- `recent` 记录按 `sessionKey`（session_id 的 hex）保存最近发现时间，`RecentDiscoverValidity=10s` 后过期：`client.go:172-176`、`:646`。

### 6.12 认证

- `handleAuth`：要求该 session 在 10s 内被最近发现；处理新会话抢占与多 AUTH_NTY 累积：`client.go:259-313`。
- `AuthAccumulator`：累积同一会话中本实例的 `device_type + ESN` 绑定，按类型去重；全部类型到齐才成功：`internal/client/auth.go:20-83`。
- 冲突判定：同一类型不同绑定 → `AUTH_CONFLICT`（带冲突详情）；跨类型 node_id 不一致或本实例不支持的赋值 → `AUTH_ASSIGNMENT_CONFLICT`；非法字段 → `INVALID_MESSAGE`：`auth.go:40-69`。
- 标识符校验：ESN 39 位数字、node/device id 非空、UTF-8、≤255 字节、不含 `/ \ : \0`：`auth.go:85-99`。
- 成功后进入 `StateAuthenticated` 并记录 `nodeID`/`bindings`：`client.go:309-311`。

### 6.13 文件传输

- `ValidateTransferStart`：认证缺失、非法文件名/哈希/分块大小/总数、文件大小越界、解压大小与工作空间校验：`internal/client/transfer.go:30-53`。
- 常量：`ChunkSize=1200`、`MaxPackageBytes=1 MiB`、`MaxExpandedSize=64 MiB`：`transfer.go:19-22`。
- `handleStart`：缓存命中直接复用；否则预检空间（乘以工作空间因子）并 `OpenTransfer` 创建 0600 临时文件 + 预分配大小：`client.go:360-439`、`transfer.go:66-90`。
- `Transfer.Accept`：校验分块序号/长度/CRC32，`WriteAt` 到偏移，进度阈值 30/60/100% 各只报告一次：`transfer.go:92-132`。
- `handleEnd`：有缺包 → 生成 `TRANSFER_LOSSPACK_REQ`；无缺包 → `Finalize`（Sync + 大小/SHA-256 校验）→ `CommitCache`：`client.go:470-507`、`transfer.go:155-185`、`:219-243`。
- `TransferProgressRsp` 在达到进度阈值时返回：`client.go:441-468`。

### 6.14 原始包缓存复用

- `CacheMatch`：`./txbz/<文件名>` 存在、常规文件、大小一致、SHA-256 一致即命中：`transfer.go:197-217`。
- `CommitCache`：校验临时文件后清空 `./txbz/` 直属普通文件并 rename 进去：`transfer.go:219-243`。
- `PruneCache`：缓存命中时保留命中文件、删除其余文件：`transfer.go:245-279`。
- 即便缓存命中，仍会重新执行解析（绝不跳过）：`client.go:412-430`。

### 6.15 缺包请求分包

- `BuildLossPackets`：把多个缺包区间按 `4 + proto.Size(Packet) ≤ 1400` 拆成多条独立 `TRANSFER_LOSSPACK_REQ`：`internal/client/messages.go:14-66`。

### 6.16 解析流程编排

- `Parser`：用容量 1 的 `gate` 通道串行化解析，避免被取消的旧会话与新会话竞争文件替换：`internal/client/parser.go:41-65`。
- `parsePackage`：可选二次空间预检 → `packagezip.Validate` → 展开大小比对 → `output.Process` → `config.UpdateSnapshot` → Linux `fsutil.SyncFilesystems`（`unix.Sync`）：`parser.go:66-127`。
- 快照原子写入是提交点，写入后即使 deadline 竞态也不回滚为失败：`parser.go:119-127`。
- 全局失败把所有类型标记同一阶段/错误码：`parser.go:129-140`。

### 6.17 通信包 ZIP 结构校验

- `Validate`：常规文件、非空、≤1 MiB、打开 ZIP：`internal/packagezip/validate.go:52-65`。
- 条目级校验：路径穿越/绝对路径/反斜杠/NUL、大小写冲突、加密条目、特殊条目、单文件≤8 MiB、解压总量≤64 MiB、压缩比≤200:1、路径/组件长度：`validate.go:78-116`、`:210-237`。
- `readAll`：实际解压并逐文件核对 CRC 与声明大小：`validate.go:165-207`。
- 限制常量集中在 `internal/packagezip/limits.go:3-13`。

### 6.18 通信包业务结构校验与设备解析

- 必选目录 `0_contacts/3_device_config/4_net_node/6_unit`：`internal/packagezip/business.go:35`。
- 索引目录只允许平铺 JSON；校验 `File.Guid`（数字或字符串统一规范化为字符串）；`contacts` 的 `UnitTree` 引用 unit/node 必须存在：`business.go:37-141`。
- `parseNode`：解析 `SystemConfiguration.LANPrimary.Server`（Server/IEC 互斥）、`LANMember.CCU/VehInter`、`Radio.MMR200/PMR200/MR9360/PRR206`，建立设备引用，每节点≤100 设备：`business.go:164-268`。
- `inferDeviceType`：按设备 ID 前缀推断类型（`dc_server_/dc_IEC_/dc_ccu_/dc_VehInter_/dc_MMR200_/dc_PMR200_/dc_MR9360_/dc_PRR206_`）：`business.go:142-161`。
- `parseDevice`：无线电设备要求存在 `1_resource`/`2_radio_subnet`，校验 `Channels` 的 `Subnet` 引用并去重、校验 `Alias`（MultiBand 必填）：`business.go:270-316`。

### 6.19 输出生成器分发

- `Process`：按 `deviceTypes` 顺序逐个处理，任一失败后后续类型标记 `SKIPPED_AFTER_PREVIOUS_FAILURE`；按设备类型路由到对应写入函数：`internal/output/output.go:35-99`。
- `validateRequest`：绑定必须匹配包内设备类型（CCUAudio 复用 CCU 配置）；Server/IEC 要求原始包名合法：`output.go:108-128`。
- 错误分级：`generationError`(GENERATE)/`writeError`(WRITE_OUTPUT)/`typeError`：`output.go:130-141`。

### 6.20 无线电输出

- `writeRadio`：计划包 = `1_resource/*` + 各 `2_radio_subnet/<subnet>.json` + 设备配置；MultiBand 额外生成 mission 包（`1_resource`、`2_radio_subnet`、`3_device_config/dc_MMR200_*|dc_PMR200_*`）：`internal/output/radio.go:19-46`。
- 输出：`plan_local.tar`（所有无线电）、`mission.tar.gz`（仅 MultiBand，实际是未压缩 tar）：`radio.go:67-74`。
- INI：`ReReadVal.Val=1`；MultiBand 追加 `MissionVersion.Val=3`、`PlanLocalUser.Val=<alias>`：`radio.go:54-65`。
- tar 由 `createTar` 生成（含父目录条目、确定性顺序）：`radio.go:81-145`。

### 6.21 CCU / CCUAudio 输出

- `writeCCU`：复制设备配置到 `./txbz_ccu/<原文件名>`，再删除目标 update 目录中匹配 `dc_ccu_*.json` 的直属普通文件，复制过去，最后清空 `txbz_ccu`：`internal/output/ccu.go:23-78`。
- 目标目录差异：CCU → 可执行目录上两级 `../../update/`；CCUAudio → 上一级 `../update/`：`ccu.go:16-22`。

### 6.22 Server / IEC 输出

- `writeServer`：重建 ZIP（保留业务目录层级、剔除/替换 `local_node.json`、写入本次 `nodeId` 的 `local_node.json`），校验后写入 `./txbz_server/<原包名>`，再解压到 `../txbz_data/<去掉.zip>/`：`internal/output/server.go:17-109`。
- `extractZIP`：清空 `txbz_data` 后按包名建子目录并解压，防止路径穿越：`server.go:111-173`。
- `verifyZIP`：校验生成的 ZIP 可读且恰好一个 `local_node.json`：`server.go:175-200`。

### 6.23 VehInter 输出

- `writeVehInter`：文件 = `0_contacts/*` + `6_unit/*` + 设备配置 + `4_net_node/<nodeId>.json`，打成 `vehinte.tar`，并更新 `ReReadVal.Val=1`：`internal/output/vehinter.go:12-38`。

### 6.24 INI 结构化更新

- `updateINI`/`setINIValue`：按 section 定位并结构化更新 `key=value`，不支持则追加新 section；保留原有行，不做整文件拼接：`internal/output/ini.go:11-59`。

### 6.25 成功快照持久化

- `UpdateSnapshot`：校验快照字段，克隆原始 JSON、更新 `deviceTypes/esn/txbzName/txbzHash/nodeId/deviceIds`，原子写成功后回填内存；失败回滚内存：`internal/config/config.go:141-174`。
- 写盘使用 `fsutil.AtomicWriteFile`（临时文件 + Sync + 跨平台替换）：`config.go:176-183`、`internal/fsutil/files.go:22-53`。

### 6.26 解析结果上报 / 重试 / ACK

- `finishParse`：生成新 `message_id`，组装 `PARSE_COMPLETE_REQ`，进入 `StateReporting`，`reportAttempts=1`，1 秒后首重试：`client.go:538-560`。
- `Tick`：未收到 ACK 时每 1 秒重发一次，最多 5 次，超限进入终态：`client.go:226-235`。
- `handleParseAck`：校验同 session、同 message_id，且 ACK 正文的 esn/result/设备类型集合与请求一致后停止重发：`client.go:562-572`。

### 6.27 会话恢复与新会话抢占

- 认证后等待传输/接收期间 30s 无有效报文 → 终态（`ClientSessionIdle`）：`client.go:213-216`。
- 新 session 的 AUTH 只有在 10s 内被最近发现过才能抢占：`client.go:260-263`。
- 接收阶段立即清理；解析阶段协作取消（`queueTakeover`）；正式文件写入无法在认证窗口内结束时回 `ERROR_CODE_BUSY`（`failBusyTakeover`）：`client.go:654-690`。
- 清理逻辑 `resetCurrent`/`finishTerminal` 会取消解析、删除未完成传输临时文件、递增 `generation`：`client.go:604-645`。

### 6.28 日志与脱敏

- 实际日志点（较少）：启动/停止、收到/丢弃报文类型、网卡初始化/发送失败、发送池刷新：`cmd/cpdc/main.go:26,95`、`internal/client/run.go:29-46`、`internal/netio/udp.go:103,172,219,238,262,306`。
- 当前实现**未**包含需求 02 §14 要求的配置读取/发现/认证/进度/解析/INI/结果重试等业务日志，也**未**实现字段级脱敏函数（`password/passwd/secret/privateKey → ***`）。
- 由于 CPDC 现有日志不输出 UDP 负载、JSON 正文或密码字段，当前无敏感信息泄漏，但「应记录的业务日志」与「脱敏规则」在代码层面是**缺失/待实现**状态。

### 6.29 构建与发布

- `build.ps1`：跨平台交叉编译 5 个产物，`Build-CPDC` 函数设置 GOOS/GOARCH/GOARM/CGO 并注入 `-X buildinfo.Target`：`build/scripts/build.ps1`。
- `build-audio.ps1`：解压/校验仓库内 go1.23.12 工具链，先 `go test ./...`，再编译 `CPDC-CCU-Audio`、`CPDC-HF`（Linux ARMv7、`GOTOOLCHAIN=local`）：`build/scripts/build-audio.ps1`。
- `build.bat`：串行调用 build.ps1 + build-audio.ps1 并保留窗口：`build/scripts/build.bat`。
- `generate-proto.ps1`：下载 buf v1.72.0、安装 protoc-gen-go v1.36.11，运行 `buf generate`：`build/scripts/generate-proto.ps1`。
- 工作空间因子：CCU 为 2×，其余 1×：`internal/buildinfo/buildinfo.go:6-9`。

### 6.30 文件系统原子操作工具

- `AtomicWriteFile`/`AtomicCopy`：临时文件 + chmod + Sync + 替换：`fsutil/files.go:22-88`。
- `ClearDirectRegularFiles`/`ClearDirectRegularFilesMatching`/`ClearDirectoryContents`：逐项清理，拒绝符号链接根目录：`files.go:89-200`。
- `CleanupKnownTemps`：只清理 CPDC 自有前缀的临时文件：`files.go:202-227`。
- `RequiredSpace`：`(raw+expanded+outputs) × factor` 再加 20% 余量：`files.go:228-246`。
- 跨平台：Windows `MoveFileEx(REPLACE_EXISTING|WRITE_THROUGH)`，Unix `os.Rename`：`replace_windows.go`、`replace_unix.go`；Linux `unix.Sync`，非 Linux no-op：`sync_linux.go`、`sync_other.go`。

---

## 7. 数据与状态

### 7.1 配置数据模型

`Config` / `Snapshot`：`internal/config/config.go:23-45`。快照四元组 `txbzName/txbzHash/nodeId/deviceIds` 只有在全部类型解析+正式输出+快照原子写成功后覆盖。

### 7.2 协议数据模型

`proto/cpd.proto` 定义 `Packet`（`session_id/message_id` + `oneof body`）与 12 类消息；`ErrorCode` 枚举 24 个错误码；`DeviceType` 枚举 9 种；`TransferStage` 4 种；`ParseStage` 7 种。

### 7.3 通信包业务数据模型

`Package/Nodes/Devices/Node/DeviceRef/Device`：`internal/packagezip/business.go:17-34`、`validate.go:41-50`。设备映射关系：`SystemConfiguration.LANPrimary.Server → Server/IEC`、`LANMember.CCU → CCU`（CPDS 侧额外派生 CCUAudio）、`LANMember.VehInter → VehInter`、`Radio.* → 四种无线电`。

### 7.4 关键算法与业务规则

- 缺包区间压缩：连续缺失块合并为闭区间 `[start,end]`：`transfer.go:134-150`。
- 进度阈值：30/60/100 各只报告一次：`transfer.go:121-131`。
- ZIP 大小/压缩比/条目数上限校验：`packagezip/limits.go`、`validate.go`。
- 工作空间预检：`required_workspace × factor`，`RequiredSpace` 另加 20% 余量：`client.go:399-404`、`fsutil/files.go:228-246`。
- ESN：128 位随机 → 39 位十进制左补零：`esn.go`。
- 脱敏规则（需求规定，代码待实现）：不记录密钥/密码正文与 UDP/JSON 正文（见 6.28）。

---

## 8. 质量与风险

### 8.1 测试情况

- 测试框架：Go 标准库 `testing`；断言含 `github.com/google/go-cmp v0.7.0`。
- 测试覆盖模块：`config`(12)、`client`(11)、`parser`(5)、`transfer`(7)、`auth`(6)、`protocol`(4)、`netio`(13)、`output`(11)、`packagezip`(11)、`fsutil`(5)（括号为 `func Test` 数量，来自 `rg "^func Test"`）。
- 实测：`go test ./...` 全部包通过（`client/config/fsutil/netio/output/packagezip/protocol` 均为 `ok`；`cmd/cpdc`、`gen/cpd/v1`、`buildinfo` 无测试文件）。
- 注意：本次运行在 Windows 下最后报 `go: failed to trim cache: open ...\go-build\trim.txt: Access is denied.`，属 Go 构建缓存权限问题，与代码无关，测试本身全部通过。

### 8.2 代码规范与约定

- 标准 Go 包布局（`cmd/` + `internal/`），跨平台用构建标签 `//go:build windows|linux|!windows|!linux` 分离。
- 错误统一使用 `ErrorCode` 枚举，不携带自然语言 reason（符合需求）。
- 生产时间参数集中在 `protocol/timing.go`，未散落硬编码。
- 所有正式输出采用原子替换（临时文件 + Sync + rename/MoveFileEx）。

### 8.3 潜在问题、技术债与风险

1. **文档与构建脚本不一致（较高）**：
   - `README.md` 声称「五个发布文件」并列出 `CPDC.exe + CPDC-CCU + CPDC-MultibandRadio + CPDC-MultibandHandheld + CPDC-HF`，但 `build.ps1` 实际构建的是 `CPDC.exe + CPDC-CCU + CPDC-MultibandRadio + CPDC-MultibandHandheld + CPDC-VehInte`（无 HF），HF 改由 `build-audio.ps1` 产出；且实际总产物为 7 个（含 `CPDC-CCU-Audio`、`CPDC-HF`）。
   - 需求 `docs/requirements/02-CPDC-requirements.md` §2 发布文件表未包含 `CPDC-VehInte`，与 `build.ps1` 不一致。
   - 二进制名 `CPDC-VehInte` 与逻辑类型名 `VehInter` 拼写不一致，需确认是有意为之还是笔误。
2. **日志与脱敏未实现（较高）**：需求 02 §14 要求的业务日志与字段级脱敏在 CPDC 代码中缺失（见 6.28）。
3. **文档笔误**：`README.md` 启动说明中「此路径是是 cpdc_config.json 路径」重复「是」。
4. **仓库根配置携带具体值**：根目录 `cpdc_config.json` 提交了已生成的 39 位 ESN（`165128925609509379179192349254119117078`）及 Linux 路径 `ReReadjsonPath:/data/user/ReReadjson.ini`、`tarPath:/data/user`。ESN 属设备标识而非密钥，风险低，但作为仓库内样例配置建议改用空 `esn`（与 `docs/examples/cpdc_config.demo.json` 一致）。
5. **ESN 数值分布**：`GenerateESN` 用 128 位随机直接转十进制再左补零，最高约 39 位；值域分布略偏（补零导致小数值概率密度与前导零长度相关），但符合需求「固定 39 位、左补零」的明确规定，属设计使然而非缺陷。
6. **测试覆盖边界**：`output`/`packagezip` 测试数量可观，但 `client` 状态机的并发/抢占场景主要由单元测试用可注入时钟覆盖，缺少真实 UDP 端到端集成测试（受限于嵌入式环境，属预期）。
7. **待确认项**：MR9360、PRR206、IEC 缺少真实样例，相关兼容性依赖 MMR200 样例体现的公共字段（需求 02 §15.25）。

---

## 9. 结论

CPDC 是一个结构清晰、职责单一的纯 Go 网络服务程序：以 UDP 广播协议完成发现/认证/分块传输/解析/结果回执，覆盖 9 种逻辑设备类型，具备防御性 ZIP 复验、原子文件替换、会话抢占与失败不回滚语义。当前实现与需求基线在核心流程上高度一致；主要缺口在于「业务日志与字段脱敏」未落地，以及「README/需求文档与构建脚本的发布目标」存在漂移，建议优先对齐文档并补全日志/脱敏实现。
