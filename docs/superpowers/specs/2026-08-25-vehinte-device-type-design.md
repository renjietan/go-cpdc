# 2026-08-25 VehInte 设备类型设计

## 目标

在 CPDC 中新增逻辑设备类型 `VehInte`，用于识别和解析以 `dc_VehInter_` 为前缀的车辆接口设备，并生成对应的 tar 产物。

本次仅修改 CPDC；CPDS 侧的 proto 与需求文档同步延后，待 CPDC 验证通过后再进行。

## 命名与约定

- 配置类型名：`VehInte`
- Proto 枚举：`DEVICE_TYPE_VEH_INTE = 9`
- 设备文件名前缀：`dc_VehInter_`
- 节点引用位置：`SystemConfiguration.LANMember.VehInter`
- 输出文件：`vehinte.tar`
- ini 触发：`ReReadJson.ini` 写入 `[ReReadVal] Val=1`

枚举号取现有最大值 `DEVICE_TYPE_CCU_AUDIO = 8` 之后的 `9`，不改变既有枚举编号，保证协议兼容。

## 组合规则

`VehInte` 为独立设备类型，不与其他类型互斥，可与 `CCU` 等共存。配置组合仍沿用现有通用规则：非空、无重复、`Server`/`IEC` 互斥、无线电类型至多一种、`CCUAudio` 必须单独。

## 变更点

### 1. 协议 proto/cpd.proto

- `DeviceType` 增加 `DEVICE_TYPE_VEH_INTE = 9;`
- 重新生成 `gen/cpd/v1/cpd.pb.go`

### 2. 配置 internal/config/config.go

- `DeviceTypeByName` 增加 `"VehInte" -> DEVICE_TYPE_VEH_INTE`
- `DeviceTypeName` 增加反向映射
- `parseTypes` 不新增互斥规则

### 3. 认证 internal/client/auth.go

- `knownDeviceType` 增加 `DEVICE_TYPE_VEH_INTE`

### 4. 包识别 internal/packagezip/business.go

- `inferDeviceType` 增加 `{"dc_VehInter_", DEVICE_TYPE_VEH_INTE}`
- `parseNode` 读取 `SystemConfiguration.LANMember.VehInter`，以 `dc_VehInter_` 前缀校验并加入设备引用
- `parseDevice` 对 `VehInte` 不执行无线电特有校验（不要求 `1_resource`、`2_radio_subnet`、`Channels`、`Subnet`、`Alias`）

### 5. 输出 internal/output

- 新增 `writeVehInte`（建议文件 `internal/output/vehinte.go`）
- tar 内容：
  - `0_contacts/` 全部文件
  - `6_unit/` 全部文件
  - `3_device_config/<绑定 deviceID>.json`（仅一个）
  - `4_net_node/<nodeID>.json`（仅一个）
- 复用 `radio.go` 的 `createTar` 生成临时 tar，校验后原子写入 `vehinte.tar`
- 更新 `ReReadJson.ini`：`[ReReadVal] Val=1`，保留无关键
- `output.Process` 增加 `DEVICE_TYPE_VEH_INTE` 分支
- `validateRequest` 对 `VehInte` 要求 `device.Type == VehInte`、`nodeID` 非空，无需 `CCUAudio` 式类型映射

### 6. 测试

- config、auth、packagezip、output 各增加 `VehInte` 用例
- output 测试校验 tar 内四个目录及筛选规则，并校验 `ReReadJson.ini` 写入 `ReReadVal=1`

## 验证

- `gofmt -w`（仅改动文件）
- `go test ./...`
- `go vet ./...`

## 暂不处理与风险

- CPDS 侧 proto 与需求文档同步（按用户决定延后）
- `generate-proto.ps1` 依赖 buf / protoc-gen-go，可能涉及网络下载
- CPDC 侧 `docs/requirements` 的更新待 CPDS 同步启动时一并处理，以保持两侧一致
