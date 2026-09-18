# CPDC Windows Build Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让统一构建脚本在保留四个Linux目标的同时生成Windows amd64发布文件 `CPDC.exe`。

**Architecture:** 继续复用 `scripts/build.ps1`中的单一构建函数，把目标操作系统改为参数；Windows和Linux只在调用参数上不同。运行时代码、配置格式和Proto均不改变。

**Tech Stack:** PowerShell、Go交叉编译、`go version -m`

---

### Task 1: 扩展统一构建脚本

**Files:**
- Modify: `scripts/build.ps1`

- [x] **Step 1: Verify the Windows artifact is currently absent**

Run:

```powershell
.\scripts\build.ps1
if (-not (Test-Path .\dist\CPDC.exe)) { throw 'CPDC.exe was not generated' }
```

Expected: FAIL with `CPDC.exe was not generated`，证明现有脚本尚未满足新发布目标。

- [x] **Step 2: Parameterize GOOS and add the Windows target**

向 `Build-CPDC`参数增加：

```powershell
[string]$GOOS = "linux",
```

并把固定赋值改成：

```powershell
$env:GOOS = $GOOS
```

在现有Linux构建调用之前新增：

```powershell
Build-CPDC -Name "CPDC.exe" -GOOS "windows" -GOARCH "amd64"
```

- [x] **Step 3: Verify all five artifacts**

Run:

```powershell
.\scripts\build.ps1
$expected = @('CPDC.exe', 'CPDC-CCU', 'CPDC-MultibandRadio', 'CPDC-MultibandHandheld', 'CPDC-HF')
foreach ($name in $expected) {
    if (-not (Test-Path (Join-Path .\dist $name))) { throw "Missing $name" }
}
go version -m .\dist\CPDC.exe
```

Expected: PASS；Windows元数据包含 `CGO_ENABLED=0`、`GOARCH=amd64`和 `GOOS=windows`。

### Task 2: 同步发布文档

**Files:**
- Modify: `README.md`
- Modify: `docs/requirements/02-CPDC-requirements.md`
- Modify: `docs/superpowers/specs/2026-07-20-cpdc-design.md`

- [x] **Step 1: Update release descriptions**

把README的“四个Linux发布文件”改成“五个发布文件”，并说明其中包括Windows amd64的 `CPDC.exe`和四个Linux目标。

在CPDC需求发布表首行增加：

```markdown
| `CPDC.exe` | `windows` | `amd64` | 无 |
```

将产品定位说明为同一源码除嵌入式Linux目标外另提供Windows amd64版本；同步旧设计文档中的发布文件数量。

- [x] **Step 2: Run complete verification**

Run:

```powershell
go test -count=1 ./...
go vet ./...
.\scripts\build.ps1
git diff --check
```

Expected: 全部成功，`dist/`包含五个发布文件。
