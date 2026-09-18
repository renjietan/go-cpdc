# CPDC Multi-Interface Broadcast Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 未指定 `--interface`时从全部合格IPv4广播接口发送CPDC回复，并且回环副本只发送一次。

**Architecture:** `internal/netio/interface.go`负责为每张合格接口生成一个稳定候选项；`internal/netio/udp.go`持有多个绑定接口地址的发送套接字并统一扇出同一数据报。客户端发现展示字段继续使用排序后的第一候选，业务状态机和Proto不变。

**Tech Stack:** Go标准库 `net`、UDP广播、现有Windows/Linux socket选项、Go testing

---

### Task 1: 多候选接口选择

**Files:**
- Modify: `internal/netio/interface_test.go`
- Modify: `internal/netio/interface.go`

- [x] **Step 1: Write failing selection tests**

将原先“多候选必须失败”的断言替换为：

```go
got, err := ChooseCandidates([]Candidate{one, two}, "")
if err != nil || len(got) != 2 || got[0].Index != one.Index || got[1].Index != two.Index {
    t.Fatalf("automatic candidates = %+v, %v", got, err)
}
```

再增加显式名称只返回一个候选、缺失名称失败，以及 `candidateForInterface`过滤非UP、非BROADCAST、回环、点对点接口并在多个IPv4中只选择排序第一项的测试。

- [x] **Step 2: Verify RED**

Run: `go test ./internal/netio -run 'TestChooseCandidates|TestCandidateForInterface' -count=1 -v`

Expected: FAIL，因为 `ChooseCandidates`和 `candidateForInterface`尚不存在。

- [x] **Step 3: Implement minimal candidate selection**

实现：

```go
func ChooseCandidates(candidates []Candidate, requested string) ([]Candidate, error)
func SelectCandidates(requested string) ([]Candidate, error)
func candidateForInterface(iface net.Interface, addresses []net.Addr) (Candidate, bool)
```

`DiscoverCandidates`每张接口最多追加一个候选；默认返回全部排序候选，显式参数仅过滤接口名。删除不再使用的单候选选择函数。

- [x] **Step 4: Verify GREEN**

Run: `go test ./internal/netio -run 'TestChooseCandidates|TestCandidateForInterface' -count=1 -v`

Expected: PASS。

### Task 2: UDP多接口扇出

**Files:**
- Modify: `internal/netio/interface_test.go`
- Modify: `internal/netio/udp.go`

- [x] **Step 1: Write failing fan-out tests**

用记录调用次数、目标地址和报文内容的假 `packetWriter`验证：两个广播writer各调用一次，loopback writer调用一次；一个writer失败时整体成功；全部writer失败时返回错误。

```go
err := writeCopies(payload, []sendTarget{
    {name: "eth0", writer: first, destination: broadcastReply},
    {name: "eth1", writer: second, destination: broadcastReply},
    {name: "loopback", writer: loopback, destination: loopbackReply},
})
```

- [x] **Step 2: Verify RED**

Run: `go test ./internal/netio -run TestWriteCopies -count=1 -v`

Expected: FAIL，因为 `writeCopies`和多发送目标结构尚不存在。

- [x] **Step 3: Implement transport fan-out**

将 `Transport.broadcast`改为带接口名称的发送套接字切片。`Listen([]Candidate)`为每个候选创建并配置一个UDP广播套接字；单个候选初始化失败时使用 `slog.Warn`记录并继续，全部失败时关闭receiver并返回错误。

`Send`在互斥锁内构造所有广播目标和一个回环目标，调用 `writeCopies`。`writeCopies`尝试全部目标、记录单目标失败，任意目标成功时返回nil，全部失败时使用 `errors.Join`返回错误。`Close`逐个关闭明确套接字。

- [x] **Step 4: Verify GREEN**

Run: `go test ./internal/netio -run 'TestWriteCopies|TestReplyDestinations' -count=1 -v`

Expected: PASS。

### Task 3: 启动流程和文档

**Files:**
- Modify: `cmd/cpdc/main.go`
- Modify: `README.md`
- Modify: `docs/requirements/02-CPDC-requirements.md`
- Modify: `docs/requirements/03-CPDS-CPDC-interaction-requirements.md`

- [x] **Step 1: Use all selected candidates**

主程序调用 `SelectCandidates`和 `Listen(candidates)`；第一候选仅提供 `current_ip/subnet_mask`展示值；启动日志输出所有发送候选。将flag帮助文本改为可选限制接口。

- [x] **Step 2: Align requirements**

明确CPDC默认使用所有UP、BROADCAST、非回环、非点对点且具有IPv4的接口；每接口一个广播副本、回环一个副本；`--interface`只用于可选限制。保留CPDS自己的网卡选择规则，不修改CPDS项目。

- [x] **Step 3: Run complete verification**

Run: `gofmt -w cmd internal; go test -count=1 ./...; go vet ./...; .\scripts\build.ps1`

Expected: 全部命令成功并生成四个Linux发布文件。

### Task 4: 发送失败后刷新网卡套接字池

**Files:**
- Modify: `internal/netio/udp_test.go`
- Modify: `internal/netio/udp.go`
- Modify: `cmd/cpdc/main.go`
- Modify: `docs/requirements/02-CPDC-requirements.md`
- Modify: `docs/requirements/03-CPDS-CPDC-interaction-requirements.md`

- [x] **Step 1: Write failing refresh tests**

新增 `TestTransportSendRefreshesFailedBroadcastsAndRetriesOnce`，用可关闭的假UDP writer验证：旧物理发送失败而回环成功时仍调用候选枚举；新池收到与原消息相同的数据报；回环只写一次；旧池在交换后关闭。新增刷新失败保留旧池、重试仍失败时只重试一次、重试后淘汰失败接口的测试。

- [x] **Step 2: Verify RED**

Run: `go test ./internal/netio -run 'TestTransportSendRefresh|TestTransportSendPrunes' -count=1 -v`

Expected: FAIL，因为 `Transport` 尚未保存候选选择规则和sender opener，`Send`尚未刷新或重试。

- [x] **Step 3: Implement one-shot refresh and retry**

将发送连接抽象为同时实现 `WriteToUDP`和 `Close`的内部接口。`Transport`保存 `requestedInterface`、`SelectCandidates`函数和sender opener。`Send`持有写锁并执行：

```text
初次发送全部物理副本和一个回环副本
→ 没有物理失败：返回
→ 有物理失败：按原选择规则枚举并创建新池
→ 创建成功后交换池、关闭旧池
→ 仅向新物理池重试原数据报一次
→ 关闭并淘汰重试仍失败的物理sender
```

初次或重试任一物理广播成功即返回成功；两次均无物理成功则返回错误。刷新失败时不替换旧池。`Close`与 `Send`共用写锁，避免关闭与交换并发。

- [x] **Step 4: Pass the requested interface into the transport**

主程序将原始 `--interface`值连同启动候选传给 `Listen`，保证自动模式刷新全部候选，显式模式刷新时仍只选择指定接口。

- [x] **Step 5: Align requirements**

在两份CPDC侧需求文档中写明错误触发刷新、相同报文单次重试、回环不重发、失败sender淘汰，以及无物理广播成功时返回网络错误。

- [x] **Step 6: Verify GREEN**

Run: `gofmt -w cmd internal; go test -count=1 ./...; go vet ./...; .\scripts\build.ps1`

Expected: 全部测试、静态检查和Windows/Linux五个发布目标构建成功。
