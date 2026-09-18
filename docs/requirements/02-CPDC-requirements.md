# CPDC 需求说明书

## 1. 文档信息

- 产品名称：Communication Plan Distribution Client（CPDC）
- 文档状态：需求基线草案
- 日期：2026-07-20
- 关联文档：[CPDS 需求说明书](./01-CPDS-requirements.md)、[CPDS—CPDC 交互流程需求说明书](./03-CPDS-CPDC-interaction-requirements.md)
- 配置样例：[cpdc_config.demo.json](../examples/cpdc_config.demo.json)

## 2. 产品定位与运行环境

CPDC 是部署在 CCU、MultiBand 和 HF 等嵌入式环境中的配置包接收与解析程序，并额外提供Windows amd64版本。同一套源代码编译为适配这些运行环境的可执行文件，实际逻辑设备类型由可执行文件同目录的 `cpdc_config.json` 决定。

CPDC 使用纯 Go 语言开发。同一套 Go 源代码通过交叉编译生成Windows amd64以及CCU、MultiBand和HF目标环境的可执行文件。业务代码和所选依赖应支持 `CGO_ENABLED=0` 构建，不依赖目标设备额外安装 C/C++ 运行库；各目标环境差异通过构建参数和运行时配置处理，不维护多套业务代码。

CPDC项目目录独立保存三份需求文档、配置样例和自己的 `proto/cpd.proto`，构建、测试和代码生成不得引用CPDS目录或工作区公共Proto文件。两项目的Proto保持相同 `package`、消息结构、字段号和枚举值，但使用各自的Go导入路径。

发布文件：

| 发布文件 | GOOS | GOARCH | 附加参数 |
|---|---|---|---|
| `CPDC.exe` | `windows` | `amd64` | 无 |
| `CPDC-CCU` | `linux` | `amd64` | 无 |
| `CPDC-MultibandRadio` | `linux` | `arm64` | `GOARM64=v8.0` |
| `CPDC-MultibandHandheld` | `linux` | `arm` | `GOARM=7` |
| `CPDC-HF` | `linux` | `arm` | `GOARM=7`，使用仓库内Go 1.23.12专用工具链 |
| `CPDC-CCU-Audio` | `linux` | `arm` | `GOARM=7`，使用仓库内Go 1.23.12专用工具链 |

所有发布文件均由相同业务源码构建；发布文件名不决定运行时逻辑设备类型，实际类型仍以 `cpdc_config.json` 为准。统一构建脚本生成Windows、CCU、MultibandRadio和MultibandHandheld四个发布文件；专用 `build/scripts/build-audio.ps1`使用仓库内Go 1.23.12生成 `build/dist/bin/CPDC-CCU-Audio`和 `build/dist/bin/CPDC-HF`。两个专用目标均为 `CGO_ENABLED=0`，且不启用 `CPDC-CCU`的2倍工作空间构建标记。

CPDC主要职责：

1. 启动时读取并校验本地配置。
2. 响应CPDS发现广播。
3. 接收并保存本实例对应的认证绑定。
4. 接收、校验和补齐原始通信保障配置包，并在 `./txbz/`保留最近一次接收成功的原始包。
5. 按本实例支持的每种设备类型分别解析通信包。
6. 向CPDS报告接收进度、缺包、传输完成和解析结果。
7. 本地解析、输出提交和成功快照写入完成后上报最终结果，并接收CPDS的结果ACK。

CPDC不提供用户交互界面，不支持暂停、取消或手动终止当前流程。

CPDC使用本项目自己的 `CPDC/internal/protocol/timing.go`集中定义近期发现有效期、会话静默、解析截止和结果重试等时间参数，不依赖或导入CPDS源码；不得在状态机、处理函数或测试代码中另行硬编码生产时间数值。

本项目运行在物理隔离且可信的节点内部广播网，CPDC不对通信包另行解密，也不对协议报文执行密码学身份认证。CRC32和SHA-256只用于发现传输损坏。

## 3. cpdc_config.json

### 3.1 结构

```json
{
  "deviceTypes": [
    "CCUAudio"
  ],
  "esn": "",
  "txbzName": "",
  "txbzHash": "",
  "nodeId": "",
  "deviceIds": {
    "CCUAudio": ""
  },
  "supportedTypes": [
    "Server",
    "HF",
    "MultiBandRadio",
    "MultibandHandheld",
    "CCU",
    "CCUAudio",
    "VehInter",
    "IEC",
    "SmallHandheld"
  ]
}
```

### 3.2 字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `deviceTypes` | 字符串数组 | 是 | 本CPDC实例支持的全部逻辑设备类型；不得为空或重复 |
| `esn` | 字符串 | 是 | 本实例共享ESN；首次可为空，生成后持久化 |
| `txbzName` | 字符串 | 是 | 上一次已在本机成功应用的原始通信包文件名；初始为空 |
| `txbzHash` | 字符串 | 是 | 上一次已成功应用的原始通信包SHA-256；初始为空，非空时为64位十六进制字符串 |
| `nodeId` | 字符串 | 是 | 上一次已成功应用的节点ID；初始为空 |
| `deviceIds` | 对象 | 是 | 上一次已成功应用的“设备类型→设备ID”映射；初始值按 `deviceTypes` 建立空字符串 |
| `supportedTypes` | 字符串数组 | 否 | 提示字段，列出当前版本支持的全部逻辑设备类型；不参与运行时身份、数量或认证判断 |

允许的设备类型：

- `Server`
- `HF`
- `MultiBandRadio`
- `MultibandHandheld`
- `CCU`
- `CCUAudio`
- `IEC`
- `SmallHandheld`
- `VehInter`

`deviceTypes` 中的多个逻辑设备共享同一个ESN。发现、认证、进度、缺包、完成和解析回复均应携带完整 `deviceTypes`，不得只回复其中一个类型。

`supportedTypes`仅用于方便用户查看可填写选项。CPDC不解析或校验该字段；现有配置读写机制将其作为未知扩展字段原样保留。业务流程、设备组合校验和协议消息只使用 `deviceTypes`。

`txbzName`、`txbzHash`、`nodeId`和 `deviceIds`共同组成“上一次已成功应用快照”。认证阶段取得的本次 `nodeId/deviceIds`只保存在会话内存中；只有本地全部类型解析、正式输出写入和成功快照原子更新均成功时，才覆盖该快照。CPDS返回的ACK只确认已经收到最终结果，不决定本地成功快照写入时机。

`deviceTypes`组合规则：

- `Server`与 `IEC`互斥，同一CPDC实例不得同时配置。
- 无线电类型集合为 `MultiBandRadio`、`MultibandHandheld`、`HF`、`SmallHandheld`；同一实例最多配置其中一种，任意两个无线电类型组合均非法，因为会竞争固定的 `plan_local.tar`、`mission.tar.gz`或 `ReReadJson.ini`输出。
- `CCU`可以与Server、IEC或一个无线电类型共存。
- `CCUAudio`用于独立的音频CPDC实例，不与同一物理CCU上的 `CCU`身份共用ESN；配置了 `CCUAudio`时，`deviceTypes`必须且只能为 `["CCUAudio"]`，不得再包含其他类型。
- Server或IEC中的一个可以与一个无线电类型共存。
- `VehInter`为独立车辆接口类型，不与其他类型互斥，可与`CCU`、一个Server/IEC或一种无线电类型共存。
- 因此，除单独的 `["CCUAudio"]`外，其他合法组合可由“可选CCU + 可选 `VehInter` + 可选且互斥的Server/IEC + 可选且至多一种无线电类型”构成；`deviceTypes`本身不得为空或包含重复值。

CPDC在启动读取配置时完成组合校验。非法组合必须记录冲突类型并以非零状态退出，不得初始化UDP端口或进入发现阶段。

### 3.3 ESN生成

当 `esn` 为空时：

1. 使用Go标准库 `crypto/rand.Reader`读取16字节密码学安全随机数，不编写操作系统专用随机数实现。
2. 转换为固定39位纯数字字符串，不足39位时左侧补零；全零值重新生成。
3. 将ESN立即、原子地写回 `cpdc_config.json`。
4. 后续启动始终复用该ESN，不得因重启重新生成。

正式发布目标均为Linux，由Go运行时通过 `crypto/rand.Reader`取得系统密码学安全随机数；开发环境即使运行在Windows上也使用相同Go API。实现必须兼容 `CGO_ENABLED=0`，不得依赖任何平台专用随机接口，也不得使用 `math/rand`、时间戳或IP地址生成ESN。

每次进程启动还应生成一个临时 `instance_nonce`。它只写入 `DISCOVER_RSP`，用于区分同一实例的重复发现回复和不同实例的ESN冲突，不写入配置文件，也不出现在认证、传输、缺包或解析消息中。

### 3.4 ESN冲突恢复

CPDC不得因网络消息、发现失败或检测到其他实例ESN相同而自动修改本机ESN。ESN冲突采用人工恢复：

1. 运维人员停止发生冲突的其中一台CPDC。
2. 将该实例 `cpdc_config.json`中的 `esn`明确修改为空字符串。
3. 重新启动CPDC；程序按第3.3节生成新ESN并原子写回配置。
4. 新ESN写回失败时，CPDC记录不含敏感正文的错误并以非零状态退出，不得使用只存在于内存、重启后会变化的身份进入网络交互。

如果CPDC在同一个5秒发现窗口内重启，CPDS可能观察到相同ESN对应两个 `instance_nonce`并安全地判定本次发现失败。设备稳定后重新发起Distribution即可，不需要清空ESN；只有冲突在稳定运行时持续存在才执行上述人工改号流程。

## 4. 启动与配置校验

CPDC启动顺序：

1. 确定可执行文件所在目录。
2. 从同目录读取 `cpdc_config.json`。
3. 校验JSON和全部字段。
4. 必要时生成并持久化ESN。
5. 创建或校验可执行文件目录下的 `./txbz/`原始包缓存目录；正常状态只允许存在一个已校验原始包，遗留会话临时文件应逐个清理。
6. 初始化固定UDP接收端口 `39001`。
7. 进入等待发现状态。

配置错误时，CPDC不得进入网络交互，应写明错误字段并以非零状态退出。

为兼容CPDS与CPDC同机部署，CPDC始终执行以下规则，无须检测或配置是否同机：

- CPDC仍作为独立进程运行，其逻辑设备身份完全由本实例的 `deviceTypes` 决定；本机CPDS不替代CPDC。
- CPDC必须将固定接收端口绑定到IPv4通配地址，同时接收来自全部本机网卡和环回接口的报文，并支持同一协议报文重复到达。默认枚举全部满足“已启用、支持广播、非回环、非点对点且至少有一个非回环IPv4地址”的接口，每张接口只选一个IPv4地址；同一接口有多个IPv4地址时按地址数值升序选择第一个。候选接口按接口索引、接口名、IPv4地址稳定排序。业务网卡名称不得硬编码；可选启动参数 `--interface <网卡名称>`仅用于诊断或显式限制为一个合格接口。没有合格接口、显式接口不合格，或所有发送套接字均初始化失败时，以 `ERROR_CODE_NETWORK_INTERFACE_ERROR`退出且不进入网络交互；部分接口初始化失败时记录告警并继续使用其余接口。
- Linux版CPDC在接收端口成功绑定后、创建候选接口发送套接字前，必须把每张候选接口的 `net.ipv4.conf.<接口名>.rp_filter`临时设为 `0`，避免跨网段广播因严格反向路径检查被内核丢弃。Linux实际校验值受 `net.ipv4.conf.all.rp_filter`影响，因此CPDC同时临时将 `conf/all/rp_filter`设为 `0`，但不得修改非候选接口自己的 `rp_filter`值；Windows版不执行任何sysctl操作。
- CPDC必须在首次修改前记录每个受管sysctl的原值。发送失败重新枚举候选接口时，必须先关闭新候选接口的 `rp_filter`再创建新发送池；处理失败时保留旧发送池并报告网络接口错误。正常退出时恢复全部原值；启动阶段部分写入失败时回滚本次新增修改并退出。异常掉电或 `SIGKILL`无法执行恢复，设备重启后由Linux系统配置重新初始化这些运行时值。若运行账号无权读取或修改所需值且当前值并非 `0`，CPDC不得进入网络交互。
- 每个UDP负载前4字节必须按网络字节序等于 `0xEEDDCCBB`（线上字节 `EE DD CC BB`）；长度不足4字节或Magic不匹配时直接丢弃且不执行Protobuf反序列化，Magic正确后才解析其后的 `Packet`。
- CPDC每次逻辑发送必须复用同一份完整数据报，从每个成功初始化的候选接口各向 `255.255.255.255` 的CPDS接收端口发送一份，并无条件只向 `127.0.0.1`发送一份环回副本。单个接口发送失败时记录告警并继续尝试其余接口和环回；回环发送成功只表示本机副本已提交，不得掩盖全部物理广播目标失败。未在本机运行CPDS时回环副本无人接收，不影响广播回复。
- 任一物理广播目标发送失败时，CPDC必须使用与启动时相同的 `--interface`选择规则重新枚举候选接口，先创建新发送套接字池，创建成功后整体替换并关闭旧池，再使用新池对原数据报重试一次。重试必须复用原 `message_id`和完整消息体，不得再次发送环回副本；每次 `Send`最多刷新一次、重试一次。
- 新池重试后，CPDC必须关闭并淘汰仍然发送失败的接口，只保留成功接口，避免未插线但仍处于启用状态的网卡导致每条消息重复刷新。以后现有成功接口再次失败时重新枚举，因此换回其他网卡仍可恢复。刷新枚举或新池创建失败时不得破坏旧池；初次或重试任一物理广播成功即完成本次外部发送，两次均没有物理广播成功时返回网络发送失败。
- CPDC只发送本地状态机生成的回复或请求，不得转发、反射或复制收到的UDP数据报，也不得因为收到RSP或ACK生成同类型回复。多网卡副本和唯一环回副本的内容及 `message_id`完全一致，数量仅由本机候选接口数决定，不会递归产生新消息。
- `DISCOVER_RSP`按 `session_id + 方向 + body类型 + message_id + ESN + instance_nonce`去重；除此之外的普通消息按 `session_id + 方向 + body类型 + message_id + ESN（适用时）`去重，其他消息不使用 `instance_nonce`；文件数据分包按 `session_id + chunk_index`幂等处理。
- 每条逻辑消息首次发送时生成一个 `message_id`；同一消息的广播副本、环回副本和重发复用该ID及相同消息体，新逻辑消息使用新ID。RSP或ACK直接复制触发NTY或REQ的ID。ID只用于消息关联和幂等，不作为业务身份或安全凭据，也不维护额外的消息ID校验机制。
- `DISCOVER_RSP.current_ip`和 `subnet_mask`仅用于前端记录，统一填写稳定排序后的第一张候选接口的IPv4地址及连续点分十进制掩码；它们不表示发现消息实际来自或回复实际发往该接口，也不参与后续业务。即使 `DISCOVER_NTY`经 `127.0.0.1`到达，也不得填写环回地址或环回掩码。
- CPDC的配置、临时文件和解析输出必须位于CPDC自己的安装目录中，不得与CPDS的导入目录、缓存目录或配置目录共用。

### 4.1 CPDS断电、崩溃与旧会话恢复

CPDC任一时刻只处理一个活动下发会话。同一二层广播域的部署前提是只有一个CPDS执行下发；CPDC仍必须保证旧会话不会阻塞下一次正确下发。

CPDC一旦为当前会话产生认证失败、传输失败或最终解析成功/失败结果，即进入该会话的终态等待。终态CPDC不再处理当前会话或其他未经过新发现的认证、START、CHUNK、END及其他业务消息，只等待新的 `session_id`对应的 `DISCOVER_NTY`。如果正在重试 `PARSE_COMPLETE_REQ`，还允许处理与该最终结果匹配的 `PARSE_COMPLETE_ACK`；收到ACK或达到最大重试次数后仍保持等待新发现状态。这样无需保留复杂的旧会话传输状态，也不会被CPDS为其他设备发送的后续广播重新激活。

接收阶段使用以下恢复规则：

1. CPDC为当前 `session_id`保存最后一次收到有效CPDS报文的单调时钟时间。
2. 在认证后等待传输、文件接收或缺包补发期间，连续30秒没有收到当前会话的有效 `AUTH_NTY`、`TRANSFER_START_NTY`、`TRANSFER_CHUNK_NTY`或 `TRANSFER_END_NTY`，判定CPDS断电或会话失效。
3. 会话失效后关闭并逐个删除该会话明确命名的未完成接收文件和暂存输出，清空临时认证绑定、接收位图、缺包信息和内存状态；不得修改正式输出、`ReReadJson.ini`、上一次成功快照及 `./txbz/`中已经写入的接收成功原始包。
4. 清理完成后立即回到等待发现状态，下一次下发不需要人工清理或重启CPDC。
5. 收到新 `session_id`的 `DISCOVER_NTY`时始终正常回复发现信息，并记录最近一次发现该会话的单调时间。只有最近10秒内收到过同一 `session_id`的发现通知、且AUTH中包含本实例完整绑定的新会话，才有资格抢占旧会话；迟到或无法对应近期发现的AUTH不得触发抢占。
6. 旧会话处于认证、等待传输、接收或缺包补发状态时，新会话的有效 `AUTH_NTY`立即取代旧会话；CPDC关闭并逐个删除旧会话未完成接收文件和暂存输出，清空临时绑定及位图后接受新认证。
7. 新会话生效后，所有迟到的旧 `session_id`报文必须忽略。

解析与结果确认阶段使用以下规则：

- CPDC已经开始解析但尚未写入正式输出时，新会话AUTH触发协作式取消；CPDC停止解压或输出生成、逐个清理暂存内容后接受新认证。解析任务必须响应内部取消信号，不得遗留后台写入任务。
- CPDC正在写正式输出时不得在单个文件写操作中途强制中断；应先让当前文件操作返回，再停止旧会话剩余处理并接受仍在认证窗口内重复到达的新会话AUTH。已经删除或写入的旧会话正式文件不恢复。无法在新会话认证窗口内结束当前文件操作时，回复失败 `AUTH_RSP`及 `ERROR_CODE_BUSY`。
- CPDC已经完成正式输出和成功快照写入、正在等待 `PARSE_COMPLETE_ACK`时，新会话AUTH立即终止旧结果重发并清理旧会话内存，但保留已经写入的正式输出、原始包缓存和成功快照，然后接受新认证。
- CPDC解析期间不应用30秒“未收到CPDS报文”的接收静默规则，但解析本身仍受30秒截止时间约束。
- CPDS在CPDC解析期间断电时，CPDC继续执行不超过30秒的本地解析。无论最终结果成功或失败，都按协议发送最多5次 `PARSE_COMPLETE_REQ`。
- 成功结果5次均未收到ACK时，停止重发并保持等待新发现状态；已经写入的正式输出和成功快照保持不变。失败结果5次均未收到ACK时停止重发并保留上一次成功快照。两种情况都只接受新 `session_id`的 `DISCOVER_NTY`，忽略其他业务消息。
- CPDC进程异常退出后重新启动时，应逐个清理能够确认属于未完成会话的临时文件和暂存输出，保留正式输出、`ReReadJson.ini`和 `cpdc_config.json`，随后正常进入等待发现状态。

## 5. 多设备类型行为

- 原始通信包只接收和校验一次。
- CPDS可能使用多条相互独立的 `AUTH_NTY`发送认证记录。CPDC按当前 `session_id`累计每条消息中属于本实例的 `device_type + ESN`记录，并按设备类型去重。
- 当本实例配置 `deviceTypes`中的每种类型都已获得唯一有效绑定后，发送 `result=RESULT_SUCCESS`且包含完整 `client/node_id/bindings`的 `AUTH_RSP`；不需要收齐或重组全网其他设备的认证记录。
- 每条绑定至少包含设备类型、当前节点ID和设备ID。
- 同一会话中相同 `device_type + ESN`重复携带相同 `node_id/device_id`时按重复消息处理；如果携带不同绑定，不得覆盖已有绑定，立即发送 `result=RESULT_FAILED`、`error_code=ERROR_CODE_AUTH_CONFLICT`的 `AUTH_RSP`并保持本次认证未完成。同一实例的不同设备类型也必须全部绑定到同一个 `node_id`，跨类型节点不一致时按相同错误码失败。该检查是对异常报文和实现错误的防御，正常CPDS必须在发送前消除冲突。
- 接收完成后，CPDC按 `deviceTypes` 中的类型执行相应解析规则。
- 只有全部类型解析成功，才能报告实例级解析成功。
- 任一类型失败时，解析结果中应列出该类型及枚举 `error_code`，实例级结果为失败。
- `PARSE_COMPLETE_REQ.type_results`必须与 `client.device_types`顺序一致，并且每种配置类型恰好一项。成功项使用 `stage=PARSE_STAGE_UNSPECIFIED`和 `error_code=ERROR_CODE_UNSPECIFIED`；直接失败项必须携带具体的 `stage/error_code`。如果在类型解析前发生全局校验或解压错误，每种类型都返回失败项并复用该全局错误码；如果因前一个类型失败而未执行后续类型，后续项使用 `PARSE_STAGE_SKIPPED/ERROR_CODE_SKIPPED_AFTER_PREVIOUS_FAILURE`。

## 6. 原始包缓存与重复处理

CPDC在可执行文件目录下使用 `./txbz/`保存“最近一次接收完整且文件大小、SHA-256校验成功”的原始ZIP，文件名保持CPDS在 `TRANSFER_START_NTY.file_name`中给出的基本文件名。正常状态只保留一个已校验原始包；不在 `cpdc_config.json`中增加缓存路径或缓存版本字段。

`./txbz/`缓存和 `cpdc_config.json`成功应用快照是两个独立状态：

- 原始包完整接收、文件大小和SHA-256校验成功后，即可更新 `./txbz/`缓存；后续解析失败不恢复更早的原始包。
- 只有解析和全部正式输出写入成功后，才能更新 `txbzName/txbzHash/nodeId/deviceIds`成功应用快照。
- 因此缓存中的包可能比成功应用快照更新，程序不得假定二者始终相同。

收到开始通知后，CPDC必须读取 `./txbz/`中的实际文件重新计算文件大小和SHA-256。只有缓存文件名等于 `file_name`且计算结果等于32字节 `file_sha256`时，才判定文件包一致；不得只相信文件名、`txbzHash`或目录中存在某个ZIP。

处理矩阵：

| 实际缓存包 | 本次认证与成功应用快照 | 行为 |
|---|---|---|
| 一致 | 任意 | 跳过文件接收；发送缓存复用成功的 `TRANSFER_COMPLETE_RSP`，再使用缓存包和本次认证进入30秒解析流程 |
| 不一致、缺失或损坏 | 任意 | 正常接收，校验成功并写入新缓存后再解析 |

AUTH阶段只暂存本次认证绑定；是否命中原始包缓存必须等收到包含 `file_name/file_sha256`的START后确定。命中缓存只能跳过文件接收，任何Distribution都不得依据成功快照跳过解析。跳过接收的实例忽略当前会话后续CHUNK和END，对重复START幂等重发相同传输结果。

成功快照只能在以下条件全部满足后更新，并且必须在第一次发送成功 `PARSE_COMPLETE_REQ`之前完成：

1. 本次使用的原始通信包已经接收并校验成功，或由 `./txbz/`实际文件校验确认可复用。
2. 所有配置设备类型解析成功。
3. 所有正式输出和 `ReReadJson.ini`写入成功。
4. 新快照原子写入成功。

CPDC在第一次发送成功 `PARSE_COMPLETE_REQ`之前，应通过一次原子配置更新同时写入：

- `txbzName`：本次开始通知中的原始文件名。
- `txbzHash`：本次开始通知中32字节 `file_sha256`的小写64位十六进制表示。
- `nodeId`：本次认证节点ID。
- `deviceIds`：本次认证的完整设备类型到设备ID映射。

不得只更新其中部分字段。解析、正式输出写入或快照写入失败时，四个快照字段全部保持上一次成功值；已经清空或写入的正式输出不回滚。未收到CPDS ACK不属于本地解析失败，不撤销已成功写入的新快照。

## 7. 文件接收

### 7.1 开始通知

首次收到有效 `TRANSFER_START_NTY`时，CPDC应：

- 根据 `session_id`创建本次传输上下文。
- 校验 `file_size`必须在1至1048576字节范围内；否则发送 `result=RESULT_FAILED`、`stage=TRANSFER_STAGE_PRECHECK`、`error_code=ERROR_CODE_PACKAGE_TOO_LARGE`的 `TRANSFER_COMPLETE_RSP`。
- 校验基本文件名、32字节SHA-256、`chunk_size/total_chunks`和认证绑定；字段非法或绑定缺失时发送PRECHECK阶段失败的 `TRANSFER_COMPLETE_RSP`及相应统一错误码。
- 先按第6节检查 `./txbz/`实际缓存。命中缓存时不创建接收文件，但仍必须从缓存执行完整解析流程。
- 未命中缓存时，读取 `expanded_size`和 `required_workspace`，在创建接收文件前检查目标文件系统可用空间；不足时发送PRECHECK阶段失败的 `TRANSFER_COMPLETE_RSP`及 `ERROR_CODE_INSUFFICIENT_STORAGE`。
- 建立实现内部使用的临时文件。
- 根据文件大小预分配或准备随机位置写入。
- 建立分包接收位图。
- 保存文件名、文件大小、分包大小、分包总数和整体SHA-256；所有这些值均来自已接受的START。

同一会话重复收到 `TRANSFER_START_NTY`时，不得重新清空临时文件或接收位图。

CPDC在尚未收到有效 `TRANSFER_START_NTY`时不得写入无法归属会话的数据分包。若初始开始通知丢失、首轮数据已经发送完毕，CPDC收到尾部或等待期重发的 `TRANSFER_START_NTY`后正常创建接收上下文；随后收到 `TRANSFER_END_NTY`时把此前未接收的全部分包报告为缺失，由CPDS完整补发。

### 7.2 分包写入

- 标准数据分包的文件负载固定为1200字节，只有最后一个分包可以小于1200字节。
- 分包总数按 `ceil(file_size / 1200)`计算，分包写入偏移按 `chunk_index * 1200`计算。
- 按 `chunk_index`计算写入偏移，不能假设UDP包按顺序到达。
- 数据分包使用与控制消息相同的“4字节Magic + Proto3 Packet”封装，`payload`最大1200字节；Packet不得超过1396字节，完整UDP负载不得超过1400字节。
- 重复分包校验通过后直接忽略，不重复计入进度。
- 单包校验失败时不写入，并在结束检查时将该序号视为缺失。
- 接收进度按“已收到的唯一有效分包数/分包总数”计算。
- 进度首次达到约30%、60%、100%时分别报告；一次跨越多个阈值时报告当前最高已达到进度。

### 7.3 缺包与完成

`TRANSFER_END_NTY`消息体为空。收到后，CPDC从当前会话已接受的START上下文取得文件名、大小、SHA-256和总分包数，并执行：

- 有缺包：将连续缺包合并为闭区间；按 `4 + proto.Size(Packet)`试装，超过1400字节时按完整区间边界生成多条相互独立的 `TRANSFER_LOSSPACK_REQ`并依次发送。每条消息都可单独处理，不增加分片编号或重组字段；该REQ没有ACK。
- 无缺包：校验文件长度和整体哈希。
- 校验成功：先把原始包以原始文件名写入 `./txbz/`，再发送 `result=RESULT_SUCCESS`、`stage=TRANSFER_STAGE_VERIFY`的 `TRANSFER_COMPLETE_RSP`并进入解析。
- 长度、哈希或缓存写入失败：发送 `result=RESULT_FAILED`的 `TRANSFER_COMPLETE_RSP`，分别使用 `TRANSFER_STAGE_VERIFY`或 `TRANSFER_STAGE_RECEIVE`及对应错误码；不得进入解析。系统不恢复旧缓存，失败后缓存目录允许为空。
- `TRANSFER_COMPLETE_RSP.message_id`按触发点关联：PRECHECK或缓存命中复制START消息ID；处理CHUNK时立即发生并上报的接收I/O错误复制该CHUNK消息ID；END触发的最终接收、大小、缓存写入或哈希结果复制END消息ID。

CPDC每次收到后续 `TRANSFER_END_NTY`都应重新检查当前位图并回复。缺包REQ丢失时，下一次结束检查重新按当前位图生成完整缺包请求；缺包重传没有固定轮数上限。已经以缓存复用或新文件校验成功结束传输的实例忽略后续END，但对重复START幂等重发传输完成结果。

### 7.4 ZIP目录与磁盘空间预检

CPDC执行两次磁盘空间检查：

1. 未命中原始包缓存时，根据 `file_size`、`expanded_size`和 `required_workspace`在创建临时文件前检查可用空间。是否采用同机空间系数由发布目标确定，不在运行时探测CPDS进程：`CPDC-CCU`按 `required_workspace × 2`检查，其他CPDC发布目标按 `required_workspace`检查。发布文件名不决定逻辑设备类型，此系数只表示目标运行环境可能与CPDS共享文件系统。
2. 原始ZIP接收并通过SHA-256校验后、开始解压前，CPDC独立读取ZIP中央目录，累计所有普通文件声明的解压后大小，并根据本实例 `deviceTypes`重新计算：

   `所需空间 = 原始ZIP + 解压内容 + 所有暂存输出 + 20%余量`

- 第二次计算结果大于CPDS声明的 `required_workspace`时，以更大的本地计算结果为准并重新检查空间。
- 空间不足时不得开始解压或替换正式文件，发送 `result=RESULT_FAILED`、`error_code=ERROR_CODE_INSUFFICIENT_STORAGE`的 `PARSE_COMPLETE_REQ`；上一次正式输出和成功快照保持不变。
- 解压过程中累计实际写入字节数。实际值超过ZIP中央目录声明值、计算溢出或超过预留空间时立即停止，删除本会话临时输出并发送 `result=RESULT_FAILED`、`error_code=ERROR_CODE_INVALID_ZIP_SIZE`的 `PARSE_COMPLETE_REQ`。
- 临时接收文件、解压目录和暂存输出必须位于已检查的目标文件系统，不能用其他挂载点的空闲空间代替。

## 8. 通信包解析通用规则

- CPDC必须对整个通信包执行防御性复验，不得只检查本实例当前绑定节点。普通ZIP即使扩展名合法也必须返回 `result=RESULT_FAILED`、`error_code=ERROR_CODE_INVALID_PACKAGE`的 `PARSE_COMPLETE_REQ`，不得写入任何正式输出目录。
- 拒绝加密ZIP、符号链接和其他特殊条目；只允许普通文件和目录。
- ZIP中每个路径必须规范化并确认位于业务根内；拒绝绝对路径、盘符、路径穿越、空路径、NUL字符、规范化后重复路径及仅大小写不同的冲突路径。
- 必须读取每个普通文件直至结束并校验ZIP CRC；条目数量、大小和累计值必须检查整数溢出。
- ZIP普通文件与目录条目总数不得超过4096，所有普通文件声明解压总量不得超过64 MiB，单个普通文件不得超过8 MiB，声明解压总量与ZIP原始大小之比不得超过200:1；超过任一限制时返回 `ERROR_CODE_INVALID_ZIP_SIZE`。
- 规范化后的相对路径UTF-8长度不得超过1024字节，单个路径组件不得超过255字节。为保证Windows和Linux解析结果一致，还应拒绝Windows保留设备名、冒号、末尾点号和末尾空格。
- 必须存在 `0_contacts`、`3_device_config`、`4_net_node`和 `6_unit`。存在任何无线电设备时还必须存在 `1_resource`和 `2_radio_subnet`；业务资源目录统一且仅允许命名为 `1_resource`，不兼容其他名称。
- `5_user`允许存在但不参与本期解析。原始通信包不包含 `local_node.json`；CPDC不依赖或解析输入中的该文件，只有Server/IEC输出流程负责创建它。若输入中意外存在同名条目，Server/IEC生成输出时以本次认证节点内容替换，保证最终只有一个该路径。
- 原始通信包业务目录直接位于ZIP根。`txbz_json_v20.zip`中的额外顶级包装目录只属于输出参考样例的历史缺陷，不是需要兼容的原始输入格式。所有生成归档均以业务根为输出根，不得产生额外包装层。
- 业务目录内全部JSON必须语法正确，关键字段类型正确；未知且不影响本期规则的字段允许忽略。`File.Guid`允许是JSON数字或字符串，读取后统一转换为字符串。
- 必须校验 `0_contacts`到 `6_unit/4_net_node`、节点到 `3_device_config`的全部引用。引用目标必须唯一存在；任一节点按CCU展开后的逻辑设备总数不得超过100个，每个原始CCU引用按 `CCU`和 `CCUAudio`两项计数。除同一原始CCU展开的这两项允许共享原始设备ID外，同一节点内设备ID不得重复，且每个 `(deviceType, deviceId)`必须唯一。
- 设备引用位置、文件名前缀和逻辑类型必须符合CPDS需求第4.4节映射，包括 `Radio.MR9360 → dc_MR9360_* → HF`、`Radio.PRR206 → dc_PRR206_* → SmallHandheld`，以及 `LANMember.VehInter → dc_VehInter_* → VehInter`。
- 无线电设备的 `Channels`必须是对象；每个频道的 `Subnet`必须是非空文件名主体，并唯一对应 `2_radio_subnet/<Subnet>.json`。多个频道允许引用同一个Subnet，生成输出时去重。
- `MultiBandRadio`和 `MultibandHandheld`必须具有非空 `Alias`；HF和SmallHandheld暂按MMR200公共结构校验，但不强制要求仅供 `PlanLocalUser`使用的Alias。
- `nodeId`、`deviceId`和原始文件基本名称的UTF-8长度均不得超过255字节；Alias不得超过128字节，不得包含NUL、回车或换行。写入INI时必须通过结构化INI写入器设置值，不得把Alias作为未经处理的整行文本拼接，避免破坏节和键结构。
- 认证消息中的 `deviceId` 对应 `3_device_config/<deviceId>.json` 的文件名主体。
- 认证消息中的 `nodeId` 用于生成 `local_node.json`。
- 输出包先在临时位置完整生成并校验，再替换正式输出。
- 删除旧文件时只能逐个删除明确的普通文件，不递归删除目录。
- 不得把未完成或校验失败的输出写入正式路径。
- 输入文件名只允许使用原始文件的基本名称，必须拒绝目录分隔符、绝对路径和路径穿越。
- 文件接收、SHA-256计算、ZIP读取和归档生成必须采用流式处理，不得要求将整个1 MiB通信包一次性载入内存。

当前缺少MR9360、PRR206和IEC真实JSON样例。首期实现按MMR200样例的公共结构推断：MR9360由 `SystemConfiguration.Radio.MR9360`引用 `dc_MR9360_*.json`，PRR206由 `SystemConfiguration.Radio.PRR206`引用 `dc_PRR206_*.json`；设备文件使用 `File/Model`和以频道编号为键的 `Channels`对象，频道对象通过 `Subnet`引用无线电子网。解析器忽略不影响本期规则的未知字段。缺少本设备解析必需字段或字段类型与该结构不兼容时返回明确解析失败，后续取得真实样例后再补充兼容规则。

CPDC发送成功 `TRANSFER_COMPLETE_RSP`后立即启动30秒本地解析截止计时。输入可以是新接收并写入 `./txbz/`的文件，也可以是文件一致而复用的缓存文件；无论成功快照是否与本次包和认证完全一致，都必须重新执行完整解析。ZIP复验、解压、全部类型输出生成、正式输出写入和成功快照写入均计入这30秒；期间不发送心跳、进度或“解析中”状态。达到截止时间仍未完成时，取消剩余操作、清理尚未写入正式路径的临时文件，并发送 `result=RESULT_FAILED`、`error_code=ERROR_CODE_PARSE_TIMEOUT`的 `PARSE_COMPLETE_REQ`；已经删除或写入的正式文件不回滚。

同一CPDC支持多个设备类型时，按以下简单顺序写入：

1. 先在临时位置生成并校验本次需要的全部新文件。
2. 依次清空各类型规定的目标文件，再写入对应新文件；不建立备份、事务清单或回滚点。
3. 任一步失败时停止剩余写入并发送 `result=FAILED`的 `PARSE_COMPLETE_REQ`。目标目录可能为空，已经写入的其他类型产物允许保留；直接失败类型记录实际阶段和错误码，尚未执行类型记录 `PARSE_STAGE_SKIPPED/ERROR_CODE_SKIPPED_AFTER_PREVIOUS_FAILURE`。
4. 只有全部类型正式输出写入成功且成功快照原子更新成功，CPDC才能发送 `result=SUCCESS`的 `PARSE_COMPLETE_REQ`。
5. 失败时不更新成功快照；下一次Distribution仍重新解析并覆盖目标文件。

## 9. MultiBandRadio 与 MultibandHandheld

对 `MultiBandRadio` 或 `MultibandHandheld`，生成 `plan_local.tar`、`mission.tar.gz` 并修改执行目录下的 `ReReadJson.ini`。

### 9.1 plan_local.tar

输出到：`./plan_local.tar`。

归档只保留包内的 `1_resource`、`2_radio_subnet`、`3_device_config`三类目录：

- `1_resource`：保留目录内全部文件。
- `2_radio_subnet`：`Channels`在当前通信包格式中是以频道编号（例如 `"01"`）为键的JSON对象；遍历其全部对象值并读取其中的 `Subnet`，只保留这些Subnet对应的JSON文件并去重。
- `3_device_config`：只保留认证消息中本设备ID对应的设备配置JSON。

其他顶层目录全部不进入输出归档。

以样例设备 `dc_MMR200_683771630` 为例：

- 设备文件：`3_device_config/dc_MMR200_683771630.json`
- Alias：`A车-MMR200`
- Channel引用：`rs_WGN-HP_1054313902`
- plan_local中应包含对应的 `2_radio_subnet/rs_WGN-HP_1054313902.json`

### 9.2 mission.tar.gz

输出到：`./mission.tar.gz`。

归档内容：

- 保留 `1_resource`中全部文件。
- 保留 `2_radio_subnet`中全部文件。
- `3_device_config`中保留所有MMR200和PMR200设备配置文件。
- 其他顶层目录不进入归档。

`mission.tar.gz`的内部格式是普通、未经过gzip压缩的tar归档。实现应先创建普通tar内容，再使用文件名 `mission.tar.gz`；不得执行gzip压缩。

### 9.3 ReReadJson.ini

修改为：

```ini
[ReReadVal]
Val=1

[MissionVersion]
Val=3

[PlanLocalUser]
Val=A车-MMR200
```

`PlanLocalUser`取本设备配置JSON中的 `Alias`。

## 10. HF 与 SmallHandheld

对 `HF` 或 `SmallHandheld`：

- 生成 `./plan_local.tar`。
- 归档筛选规则与第9.1节相同：全部 `1_resource`、本设备Channels引用的 `2_radio_subnet`文件以及本设备对应的 `3_device_config`文件。
- HF设备配置文件名匹配 `dc_MR9360_*.json`；SmallHandheld设备配置文件名匹配 `dc_PRR206_*.json`。
- 修改执行目录下的 `ReReadJson.ini`。
- `ReReadJson.ini`只要求将以下值改为1：

```ini
[ReReadVal]
Val=1
```

不得为仅支持上述结构的HF配置文件强行增加 `MissionVersion` 或 `PlanLocalUser`。

如果 `ReReadJson.ini` 不存在：

- `MultiBandRadio`或 `MultibandHandheld` 创建包含 `ReReadVal=1`、`MissionVersion=3`和本设备Alias对应 `PlanLocalUser`的完整文件。
- `HF`或 `SmallHandheld` 创建只包含 `[ReReadVal]`及 `Val=1`的文件。

如果文件已存在，只更新对应键并保留无关节、无关键和值。

## 10.1 VehInter

对 `VehInter`：

- 生成 `./vehinte.tar`。
- 归档内容包含：
  - `0_contacts/` 下全部文件；
  - `6_unit/` 下全部文件；
  - 认证绑定设备对应的 `3_device_config/dc_VehInter_*.json`；
  - 当前节点对应的 `4_net_node/<nodeId>.json`。
- 修改执行目录下的 `ReReadJson.ini`，仅将 `[ReReadVal] Val` 置为 `1`，不写 `MissionVersion` 或 `PlanLocalUser`。
- 上述全部步骤成功后才允许更新成功应用快照并报告 `VehInter`解析成功。
- `VehInter`输出不建立回滚机制；任一步失败时保留已写入内容并整体报告失败，不更新成功快照。

## 11. CCU

对 `CCU`：

1. 根据认证消息中的设备ID定位对应配置，例如 `3_device_config/dc_ccu_10.64.0.1.json`。
2. 如果 `./txbz_ccu`不存在则创建。
3. 递归删除 `./txbz_ccu`中的全部文件和子目录，但保留 `txbz_ccu`根目录本身。
4. 将本CCU设备配置文件复制到 `./txbz_ccu/<原文件名>`。
5. 输出文件内容必须与原通信包中的对应JSON一致。
6. 将 `../../update/`解析为CPDC可执行文件目录的上两级目录下的 `update`目录；目录不存在时创建。
7. 复制新配置前，逐个删除 `../../update/`直属目录下文件名匹配 `dc_ccu_*.json`的普通文件；不递归查找或删除，不处理子目录，也不影响其他文件。
8. 将 `./txbz_ccu/<原文件名>`复制到 `../../update/<原文件名>`，复制后的文件内容必须与原通信包中的对应JSON一致。
9. 复制成功后，递归删除 `./txbz_ccu`中的全部文件和子目录，但保留 `txbz_ccu`根目录本身；成功状态下配置文件只保留在 `../../update/`。
10. 上述步骤全部成功后才允许更新成功应用快照并报告 `CCU`解析成功。

`CCU`输出不建立回滚机制。清理或复制任一步骤失败时整体解析失败且不得更新成功应用快照；已经删除、生成或复制的文件保持现状。

对 `CCUAudio`，使用与 `CCU`相同的设备配置定位和内容校验规则，但由单独运行、具有独立ESN的CPDC实例执行。路径均以CPDC可执行文件所在目录为基准，按以下顺序处理：

1. 根据认证消息中的设备ID定位对应的 `3_device_config/dc_ccu_*.json`；该绑定可与配对的 `CCU`实例共享同一个原始设备ID。
2. 如果 `./txbz_ccu`不存在则创建；递归删除其中全部文件和子目录但保留 `txbz_ccu`根目录，然后把本CCU设备配置复制到 `./txbz_ccu/<原文件名>`。
3. 将 `../update/`解析为CPDC可执行文件目录的父目录下的 `update`目录；目录不存在时创建。`CCUAudio`的发布目录固定为 `../update/`，与 `CCU`的 `../../update/`不同。
4. 在复制新文件前，逐个删除 `../update/`直属目录下文件名匹配 `dc_ccu_*.json`的普通文件；不递归查找或删除，不影响其他文件及子目录。
5. 将 `./txbz_ccu/<原文件名>`复制到 `../update/<原文件名>`，文件内容必须与原通信包中的对应JSON一致。
6. 复制到 `../update`成功后，递归删除 `./txbz_ccu`中的全部文件和子目录但保留根目录，使成功状态下该目录为空。
7. 上述步骤全部成功后才允许更新成功应用快照并报告 `CCUAudio`解析成功。

`CCUAudio`输出不建立回滚机制。复制到 `../update`失败时整体解析失败，保留 `./txbz_ccu`中已经生成的文件；复制成功但随后清空 `./txbz_ccu`失败时，保留 `../update`中已经写入的文件并整体报告失败。任何失败均不得更新成功应用快照，已经删除、生成或复制的文件保持现状。

## 12. Server 与 IEC

对 `Server` 或 `IEC`：

IEC与Server使用相同解析规则，但设备文件命名不同：IEC匹配 `dc_IEC_*.json`，Server匹配 `dc_server_*.json`。二者在节点配置中都位于 `SystemConfiguration.LANPrimary.Server`数组；CPDC以认证消息中的 `deviceType`和 `deviceId`定位对应文件，不得把 `dc_IEC_*`误判为Server。

1. 复制业务根下的完整原始通信包内容，并保持相对于业务根的原有目录层级；输出不得增加额外顶级包装目录。
2. 在包含 `0_contacts`、`1_resource`等业务目录的同一级创建 `local_node.json`。原始通信包按规范不存在该文件；若异常输入中已有同名条目，则防御性替换为本次认证的 `nodeId`内容，输出ZIP中只能存在一个该路径的文件。
3. `local_node.json`内容为：

```json
{
  "LocalNode": "<认证消息中的nodeId>"
}
```

4. 输出ZIP文件名保持原始通信包文件名。
5. 输出目录为 `./txbz_server/`；目录不存在时创建。
6. 新包生成并校验成功后，逐个删除 `./txbz_server/`下全部直属普通文件，不递归删除子目录。
7. 将新包写入 `./txbz_server/<原始通信包文件名>`。
8. 对 `Server`和 `IEC`，额外将 `../txbz_data/`解析为CPDC可执行文件目录的父目录下的 `txbz_data`目录；目录不存在时创建。以原始通信包文件名去掉末尾 `.zip`扩展名作为本次解压子目录名，例如 `txbz_123.zip`对应 `../txbz_data/txbz_123/`。
9. 解压前，递归删除 `../txbz_data/`中的全部文件和子目录，但保留 `txbz_data`根目录本身。
10. 清理完成后创建 `../txbz_data/<原始通信包文件名去掉.zip>/`，将已经生成并校验成功、包含本次 `local_node.json`的Server或IEC输出ZIP完整解压到该子目录，保持ZIP内相对目录结构；不得把ZIP内容直接平铺到 `../txbz_data/`根目录。该子目录内的内容必须与 `./txbz_server/<原始通信包文件名>`一致。

Server和IEC额外解压不建立回滚机制。清理 `../txbz_data/`或解压任一步骤失败时，对应类型解析失败且不得更新成功应用快照；已经写入 `./txbz_server/`或 `../txbz_data/`的内容保持现状。只有 `./txbz_server/`输出和 `../txbz_data/`解压全部成功后，才允许报告Server或IEC解析成功。

参考包 `txbz_json_v20.zip` 是Server/IEC的CPDC输出产物参考，在业务内容上比原始UAE包增加了 `local_node.json`。其中ZIP内部的同名顶级包装目录属于样例缺陷，实际生成的ZIP不得复现；`../txbz_data/`下按ZIP文件名创建的解压子目录属于文件系统输出布局，不写入ZIP内部。该文件不作为CPDC原始输入格式兼容依据。

## 13. 解析结果确认

CPDC只使用 `PARSE_COMPLETE_REQ`上报解析最终结果：

- 成功：`result=RESULT_SUCCESS`，通过 `client`携带ESN和完整 `device_types`，并携带节点ID、完整绑定和每种类型的成功结果。CPDC必须在本次Distribution中重新完成解析、全部正式输出写入和成功快照更新后，才能第一次发送该REQ。
- 失败：`result=RESULT_FAILED`，携带ESN、完整 `device_types`、节点ID、统一枚举 `error_code`和各类型失败结果。失败不得更新成功快照，但不回滚已经清空或写入的正式输出。

成功和失败结果均每隔1秒重发一次，最多发送5次，始终复用相同 `session_id/message_id`和相同消息体。CPDS对两种结果统一回复 `PARSE_COMPLETE_ACK`，其两个ID直接复制对应REQ。CPDC先使用相同 `session_id/message_id`关联ACK，再校验ACK正文中的ESN、设备类型集合和结果与当前最终REQ一致后停止重发；不对 `message_id`增加额外业务含义或安全校验。

- 收到有效ACK：停止重发并保持终态，只等待新 `session_id`的发现通知。
- 5次均未收到ACK：停止重发并保持终态。成功结果已经写入的正式输出和成功快照保持不变；失败结果继续保留上一次成功快照。除新的发现通知外，其他业务消息全部忽略。

CPDS重复ACK和CPDC重复发送最终结果均不得产生重复副作用。最终结果不是解析过程进度，协议不定义独立解析失败消息。

## 14. 日志与错误

CPDC至少记录：

- 配置读取和ESN生成结果。
- 发现、认证和绑定信息。
- 文件开始信息、接收进度、缺包区间和整体校验结果。
- 每种设备类型的解析开始、输出文件和结果。
- ReReadJson.ini修改结果。
- `PARSE_COMPLETE_REQ`最终结果、重试次数和ACK结果。

CPDC日志和协议结构化字段统一执行以下脱敏规则：

- 不记录 `1_resource`中的密钥正文，也不记录设备配置中的密码正文，包括 `AdvancedPassword`和 `PrimaryPassword`。
- 对字段名包含 `password`、`passwd`、`secret`、`privateKey`或明确表示密钥内容的字段，按大小写不敏感规则把字段值替换为 `***`；脱敏必须在结构化数据序列化为日志或错误文本之前完成。
- 不记录原始UDP分包负载、完整ZIP内容或完整JSON正文。
- JSON解析失败时只记录文件路径、字段路径、错误类型和位置，不输出可能包含敏感内容的原文片段。
- `PARSE_COMPLETE_REQ`等失败消息只携带枚举 `error_code`，不得携带自然语言原因或从输入文件截取的原文。
- 文件名、设备ID、节点ID、SHA-256和分包序号允许正常记录。

## 15. 验收标准

1. ESN为空时使用Go `crypto/rand.Reader`生成128位随机值，转换为固定39位纯数字ESN并立即写回；重启后保持不变，且全部Linux目标可在 `CGO_ENABLED=0`下运行。
2. 多类型实例在所有回复中携带完整类型数组，并按每种类型执行解析。
3. 重复 `TRANSFER_START_NTY`不会清空已接收数据。
4. 能检测缺包并持续报告，直至收齐或CPDS判定设备失联。
5. `plan_local.tar`、`mission.tar.gz`、CCU配置和Server/IEC包符合各自筛选规则。
6. `mission.tar.gz`实际为普通tar，不进行gzip压缩。
7. 本地成功时在第一次发送成功 `PARSE_COMPLETE_REQ`前原子更新 `txbzName`、`txbzHash`、`nodeId`和 `deviceIds`；5次均未收到ACK时仍保留成功输出和新快照。
8. `./txbz/`实际缓存的文件名和SHA-256与START一致时只跳过文件接收，随后仍从缓存重新解析；缓存不一致时正常接收并在校验成功后解析。任何成功快照都不得跳过本次解析。
9. 与CPDS同机部署时，同时收到广播报文和环回副本不会重复写入文件、累计进度或执行解析。
10. 经环回接口收到 `DISCOVER_NTY`时，`DISCOVER_RSP.current_ip/subnet_mask`仍来自稳定排序后的第一张候选接口；`instance_nonce`只出现在发现回复，后续消息的 `ClientIdentity`只包含ESN和完整设备类型。
11. CPDS与CPDC使用不同安装目录时，CPDC清理和替换输出不会影响CPDS导入的原始通信包及配置。
12. CPDS在文件接收阶段断电后，CPDC能够在30秒静默超时或新会话认证到达时清理旧会话，并正常完成下一次下发。
13. 多类型实例任一类型解析或写入失败时上报 `result=FAILED`的 `PARSE_COMPLETE_REQ`且不更新成功快照；目标目录可能为空，已完成写入的其他类型产物允许保留，不执行回滚；未执行类型使用 `PARSE_STAGE_SKIPPED/ERROR_CODE_SKIPPED_AFTER_PREVIOUS_FAILURE`。
14. `ReReadJson.ini`不存在时，能够按设备类型创建所需内容。
15. 多条独立 `AUTH_NTY`重复或乱序到达时，CPDC能够按当前会话累计自己的全部设备类型绑定，且不会重复绑定。
16. 大量缺包区间能够按 `4 + proto.Size(Packet)`拆分为多个不超过1400字节的独立 `TRANSFER_LOSSPACK_REQ`；CPDS不回复ACK但逐条幂等合并，丢失REQ可由下一次空END触发重新上报。
17. 认证类型为IEC且设备ID为 `dc_IEC_*`时，能够按Server/IEC规则生成包含 `local_node.json`的节点包。
18. `TRANSFER_START_NTY.file_size`超过1 MiB或开始处理前目标文件系统空间不足时，不创建接收文件，并返回PRECHECK阶段失败的 `TRANSFER_COMPLETE_RSP`及对应错误码。
19. `CPDC-CCU`始终按 `required_workspace × 2`检查可用空间，其他CPDC发布目标按 `required_workspace`检查；不通过进程探测或 `deviceTypes`猜测是否与CPDS同机。
20. ZIP中央目录声明值与实际解压写入量不一致、大小计算溢出或处理期间空间不足时，不替换任何正式输出，并返回统一错误码。
21. 普通ZIP或不满足通信配置包目录、引用和业务字段规则的ZIP返回 `result=RESULT_FAILED`、`error_code=ERROR_CODE_INVALID_PACKAGE`的 `PARSE_COMPLETE_REQ`，不会生成任何正式文件或写入正式输出目录。
22. 丢失首部两次 `TRANSFER_START_NTY`且忽略全部首轮数据时，CPDC能由尾部开始通知创建上下文，在收到 `TRANSFER_END_NTY`后报告全部分包缺失，并通过补发完成接收。
23. 同一会话相同 `device_type + ESN`收到不同 `node_id`或 `device_id`，或者同一多类型实例的绑定具有不同 `node_id`时，CPDC不覆盖已有绑定，返回 `result=RESULT_FAILED`、`error_code=ERROR_CODE_AUTH_CONFLICT`的 `AUTH_RSP`且不进入传输阶段。
24. 原始通信包不依赖或解析 `local_node.json`；Server/IEC输出流程使用本次认证节点ID创建该文件，输出业务根下最终只有一个 `local_node.json`。
25. 在缺少MR9360、PRR206和IEC真实样例的条件下，兼容MMR200样例所体现的公共字段；推断所需字段缺失或类型不兼容时给出明确解析失败，不产生部分正式输出。
26. 文件完整后30秒内完成解析、正式输出写入和成功快照写入；达到截止时间时停止剩余操作且不回滚已写入文件，并上报 `result=RESULT_FAILED`、`error_code=ERROR_CODE_PARSE_TIMEOUT`的 `PARSE_COMPLETE_REQ`。
27. `PARSE_COMPLETE_REQ`同时覆盖成功和失败结果，CPDS统一回复 `PARSE_COMPLETE_ACK`；协议中不存在独立解析失败消息。
28. 文件接收并校验成功后以原始文件名保存到 `./txbz/`，即使随后解析失败也保留该缓存且不更新成功应用快照。
29. `TRANSFER_END_NTY`消息体为空；文件信息只取自已接受的START。协议中不存在 `TRANSFER_LOSSPACK_ACK`、`TRANSFER_REJECTED_RSP`或 `TRANSFER_VERIFY_FAILED_RSP`。
30. CPDC使用 [`proto/cpd.proto`](../../proto/cpd.proto)生成的Go类型完成Packet编解码；Magic由UDP封装层处理。Magic不匹配的消息不执行Protobuf解析，Packet内部不依赖 `magic/version/timestamp/nty_message_id/req_message_id`字段。
31. 新会话有效AUTH能够按旧会话阶段安全抢占：接收阶段立即清理，解析暂存阶段协作取消，正式文件写入阶段等待当前单文件操作返回后停止剩余处理，等待最终ACK阶段停止旧结果重发；不回滚旧会话已经删除或写入的正式文件。
32. 无论包、nodeId、deviceId和成功快照是否一致，每次Distribution都实际执行解析并在30秒内上报本次结果。
33. CPDC防御性复验能够拒绝加密ZIP、特殊条目、路径穿越、重复或大小写冲突路径和CRC错误，并且不修改正式输出或成功快照。
34. 当前绑定节点本身有效但通信包中其他节点存在引用缺失时，CPDC仍以 `ERROR_CODE_INVALID_PACKAGE`报告解析失败。
35. `File.Guid`为JSON数字或字符串时均能规范化为字符串处理；未知且不影响本期规则的字段不会导致误判。
36. 多频道重复引用同一个Subnet时复验成功且输出去重；HF和SmallHandheld分别从 `Radio.MR9360`和 `Radio.PRR206`取得设备绑定。
37. 设备配置含密码或密钥字段、或者JSON解析错误发生在敏感字段附近时，CPDC日志不包含原始敏感正文，`PARSE_COMPLETE_REQ`只携带枚举错误码。
38. 修改 `CPDC/internal/protocol/timing.go`后，CPDC会话失效、解析截止和结果重试行为同步变化；不存在散落的生产时间硬编码，也不依赖CPDS工程文件。
39. CPDC不会因收到网络消息或发现冲突自动修改ESN；人工清空 `esn`并重启后生成、原子持久化新ESN，持久化失败时不进入网络交互。
40. `Server + IEC`、任意两个无线电类型、空数组或重复类型均在启动时校验失败并阻止网络初始化；`CCU + Server + MultiBandRadio`等不冲突组合可以正常启动。
41. `ClientIdentity`只编码ESN和完整设备类型；发现回复直接编码ESN、instance_nonce、完整设备类型、IP和掩码。解析结果为每个配置类型生成且仅生成一条有序 `type_results`。
42. 所有协议失败结果只携带非零 `ErrorCode`枚举，不携带 `reason`或其他自然语言失败文本；成功结果使用 `ERROR_CODE_UNSPECIFIED`。
43. 同一逻辑消息的广播、环回和重发复用一个 `message_id`，RSP/ACK复制触发消息的ID；CPDC不增加额外ID字段或额外消息ID业务校验，但接收最终ACK时仍校验正文中的ESN、设备类型集合和结果。
44. CPDC无条件发送广播回复和相同的环回副本；本机没有CPDS时不影响广播流程，本机存在CPDS时不依赖广播自回送。
45. `DISCOVER_RSP`使用包含 `instance_nonce`的专用去重键；其他消息不携带也不使用nonce。同一ESN、不同nonce的两个发现回复均能保留并被CPDS识别为冲突。
46. CPDC产生认证失败、传输失败或最终解析结果后只等待新 `session_id`的发现通知；最终REQ重试期间可额外接收匹配ACK，CPDS为其他设备发送的旧会话消息全部忽略。
47. 处理CHUNK时发生即时接收I/O错误的 `TRANSFER_COMPLETE_RSP`复制对应CHUNK消息ID；START预检/缓存结果和END最终校验结果分别复制START与END消息ID。
48. ZIP条目数、声明解压总量、单文件大小、压缩比、路径长度或跨平台路径名称超过上限时以规定错误码失败，不修改正式输出或成功快照。
49. CPDC不硬编码业务网卡名称；默认从每张合格IPv4广播接口各发送一份，可用 `--interface`显式限制为一张。部分接口初始化或发送失败不影响其他接口；物理发送失败时按相同选择规则刷新发送池并用原报文重试一次，刷新失败不破坏旧池。没有可用发送接口时拒绝启动网络交互。发现回复只报告稳定排序后的第一张启动候选接口的IP和掩码。
50. CPDC不会转发或反射收到的数据报；每个本地逻辑消息每次发送尝试在每张候选接口最多产生一个广播副本、全机只产生一个环回副本，物理错误恢复最多额外重试一次且不重发环回。所有副本完全相同且不会触发再次复制，不形成消息环路或广播风暴。
51. 统一构建脚本一次生成Windows amd64的 `CPDC.exe`以及 `CPDC-CCU`、`CPDC-MultibandRadio`、`CPDC-MultibandHandheld`三个Linux发布文件；四个产物均为 `CGO_ENABLED=0`，Windows版本使用普通1倍工作空间预检且不携带CCU构建标记。
52. Linux版CPDC在初始建池和动态刷新建池前，将 `conf/all/rp_filter`及所有当前候选接口的 `rp_filter`设为 `0`；不修改非候选接口自己的值。正常退出恢复原值，写入失败回滚本次修改且不得用未处理的新接口替换旧发送池。Windows版不执行该操作。
53. `deviceTypes: ["CCUAudio"]`能够使用独立ESN完成发现和认证；解析成功后配置文件只保留在相对可执行文件目录的 `../update/`且 `./txbz_ccu`为空。复制或最终清理失败时不回滚已完成文件操作、不更新成功快照，并上报解析失败。
54. `CCU`解析成功后配置文件只保留在相对可执行文件目录的 `../../update/`，且 `./txbz_ccu`为空；写入前只删除 `../../update/`直属目录下匹配 `dc_ccu_*.json`的普通文件，不递归且不影响子目录或其他文件。任一步失败不回滚且不更新成功快照。
55. `Server`和 `IEC`除保留 `./txbz_server/<原始通信包文件名>`外，还会在递归清空并保留 `../txbz_data/`根目录后，把包含本次 `local_node.json`的完整输出包解压到 `../txbz_data/<原始通信包文件名去掉.zip>/`；ZIP内容不得直接平铺到 `txbz_data`根目录。清理或解压失败时不回滚且不更新成功快照。
56. `build/scripts/build-audio.ps1`使用 `build/compiler/`中的Go 1.23.12工具链生成Linux ARMv7、`GOARM=7`、`CGO_ENABLED=0`的 `build/dist/bin/CPDC-CCU-Audio`和 `build/dist/bin/CPDC-HF`，两者均不启用 `CPDC-CCU`的2倍工作空间构建标记；原统一构建脚本不再生成HF，其余四个产物保持不变。
