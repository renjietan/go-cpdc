# CPDC 接口 rp_filter 管理设计

## 目标

Linux 版 CPDC 在跨网段接收广播时，避免严格反向路径检查丢弃源地址不符合本机反向路由的 UDP 报文。CPDC 必须只管理自己实际使用的广播接口，不硬编码 `eth0`、`eth1` 或网桥名称；Windows 版行为不变。

## 方案

CPDC 的 UDP Transport 在接收端口成功绑定后创建 `rp_filter` 控制器。控制器在发送套接字建立前，对当前候选接口设置 `net.ipv4.conf.<interface>.rp_filter=0`。Linux 的实际校验值取接口值与 `net.ipv4.conf.all.rp_filter` 的较大值，因此控制器也将 `conf/all/rp_filter` 临时设为 `0`，否则仅修改接口值不能保证关闭检查。

候选接口仍沿用现有规则：默认选择所有已启用、支持广播、非回环、非点对点且拥有 IPv4 地址的接口；使用 `--interface` 时仅选择指定接口。发送失败触发接口池刷新时，在新套接字建立前同步处理新候选接口。

控制器首次接触每个 sysctl 时记录原值。CPDC 正常关闭时恢复所有原值；启动或动态刷新期间写入失败时回滚本次新增修改并返回明确错误。若值已为 `0`，只读取和记录，不执行无意义写入。异常掉电或 `SIGKILL` 无法执行恢复，但 sysctl 为运行时内核状态，设备重启后由系统配置重新初始化。

## 代码边界

- `internal/netio/rpfilter.go`：平台无关的控制器、原值记录、失败回滚和恢复逻辑。
- `internal/netio/rpfilter_linux.go`：Linux `/proc/sys/net/ipv4/conf` 读写与生产工厂。
- `internal/netio/rpfilter_other.go`：非 Linux 空实现，保证 Windows 构建不要求管理员权限或修改系统设置。
- `internal/netio/udp.go`：在初始建池、刷新建池和关闭生命周期中调用控制器。

不增加配置项，不运行 `sysctl` 外部命令，不持久修改 `/etc/sysctl.conf`，也不改变 UDP 协议报文。

## 错误处理

- 初始读取或写入失败：关闭已打开的接收套接字，回滚本次修改并使 CPDC 启动失败。
- 刷新时处理新接口失败：保留旧发送池，当前发送按既有网络失败路径返回错误。
- 正常退出恢复失败：继续关闭网络资源并合并返回恢复错误，日志可见具体 sysctl 路径。

## 验证

使用内存假的 sysctl 读写器覆盖：目标接口和 `all` 均置零、重复处理不覆盖原值、部分写入失败回滚、关闭恢复、关闭幂等。Transport 测试覆盖初始建池前调用、刷新失败保留旧池以及关闭恢复。最后运行 `go test ./...`、`go vet ./...`、Windows/Linux 全目标构建。
