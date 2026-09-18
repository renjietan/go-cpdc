# VehInte 设备类型实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 CPDC 中新增 `VehInte` 设备类型：按 `dc_VehInter_` 前缀识别，生成 `vehinte.tar`，并更新 `ReReadJson.ini`。

**Architecture:** 复用现有设备类型扩展点：新增 proto 枚举；配置映射与认证白名单各加一项；`packagezip` 增加前缀与 `LANMember.VehInter` 引用解析；`output` 新增 `writeVehInte`，复用 `createTar` 与 `updateINI`。

**Tech Stack:** Go 1.25、protobuf（buf + protoc-gen-go）、标准库 archive/tar、archive/zip。

> 说明：当前工作区不是 git 仓库，因此各任务的 “Commit” 步骤替换为“运行本包测试确认通过”的检查点；不执行 `git add/commit`。

---

## Task 1: 协议枚举与生成代码

**Files:**
- Modify: `proto/cpd.proto`
- Regenerate: `gen/cpd/v1/cpd.pb.go`

- [ ] **Step 1: 在 proto 中新增枚举**

在 `enum DeviceType` 的 `DEVICE_TYPE_CCU_AUDIO = 8;` 之后增加：

```proto
  DEVICE_TYPE_VEH_INTE = 9;
```

- [ ] **Step 2: 重新生成 protobuf 代码**

Run:

```powershell
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File .\build\scripts\generate-proto.ps1
```

Expected: 命令成功退出。

- [ ] **Step 3: 确认生成结果**

Run:

```powershell
rg -n "DEVICE_TYPE_VEH_INTE" gen\cpd\v1\cpd.pb.go
```

Expected: 输出包含 `DeviceType_DEVICE_TYPE_VEH_INTE`。

---

## Task 2: 配置映射

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/config/config_test.go` 末尾增加：

```go
func TestLoadAcceptsVehInte(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, map[string]any{
		"deviceTypes": []string{"VehInte"},
		"esn":         strings.Repeat("3", 39),
		"deviceIds":   map[string]string{"VehInte": ""},
	})

	cfg, err := Load(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.DeviceTypes) != 1 || cfg.DeviceTypes[0] != cpdv1.DeviceType_DEVICE_TYPE_VEH_INTE {
		t.Fatalf("device types = %v, want VehInte(9)", cfg.DeviceTypes)
	}
	if got := DeviceTypeName(cfg.DeviceTypes[0]); got != "VehInte" {
		t.Fatalf("device type name = %q", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run:

```powershell
go test ./internal/config -run TestLoadAcceptsVehInte -count=1
```

Expected: FAIL（`unknown device type "VehInte"`）。

- [ ] **Step 3: 实现映射**

在 `internal/config/config.go` 的 `DeviceTypeByName` map 中增加：

```go
"VehInte": cpdv1.DeviceType_DEVICE_TYPE_VEH_INTE,
```

在 `DeviceTypeName` map 中增加：

```go
cpdv1.DeviceType_DEVICE_TYPE_VEH_INTE: "VehInte",
```

`parseTypes` 无需修改，`VehInte` 不参与互斥判断。

- [ ] **Step 4: 运行确认通过**

Run:

```powershell
go test ./internal/config -run TestLoadAcceptsVehInte -count=1
```

Expected: PASS。

---

## Task 3: 认证白名单

**Files:**
- Modify: `internal/client/auth.go`
- Test: `internal/client/auth_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/client/auth_test.go` 末尾增加：

```go
func TestAuthAccumulatorAcceptsVehInteAssignment(t *testing.T) {
	esn := "123456789012345678901234567890123456789"
	deviceType := cpdv1.DeviceType_DEVICE_TYPE_VEH_INTE
	auth := NewAuthAccumulator(esn, []cpdv1.DeviceType{deviceType})
	decision := auth.Apply(&cpdv1.AuthNty{Assignments: []*cpdv1.DeviceAssignment{
		{DeviceType: deviceType, Esn: esn, NodeId: "node-1", DeviceId: "dc_VehInter_1"},
	}})
	if !decision.Done || decision.Result != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("decision = %+v", decision)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run:

```powershell
go test ./internal/client -run TestAuthAccumulatorAcceptsVehInteAssignment -count=1
```

Expected: FAIL（`INVALID_MESSAGE`，因 `knownDeviceType` 未包含 VehInte）。

- [ ] **Step 3: 实现白名单**

在 `internal/client/auth.go` 的 `knownDeviceType` switch 中增加：

```go
cpdv1.DeviceType_DEVICE_TYPE_VEH_INTE,
```

- [ ] **Step 4: 运行确认通过**

Run:

```powershell
go test ./internal/client -run TestAuthAccumulatorAcceptsVehInteAssignment -count=1
```

Expected: PASS。

---

## Task 4: 包识别

**Files:**
- Modify: `internal/packagezip/business.go`
- Test: `internal/packagezip/validate_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/packagezip/validate_test.go` 末尾增加：

```go
func TestValidateRecognizesVehInteDevice(t *testing.T) {
	entries := validEntries()
	entries["3_device_config/dc_VehInter_1.json"] = []byte(`{"Model":"VehInte"}`)
	entries["4_net_node/nn_1.json"] = []byte(`{
      "File":{"Guid":1001},
      "SystemConfiguration":{
        "LANMember":{"CCU":["dc_ccu_1"],"VehInter":["dc_VehInter_1"]},
        "LANPrimary":{"Server":[]},
        "Radio":{}
      }
    }`)

	pkg, err := Validate(context.Background(), writeZIP(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	defer pkg.Close()

	device, ok := pkg.Devices["dc_VehInter_1"]
	if !ok || device.Type != cpdv1.DeviceType_DEVICE_TYPE_VEH_INTE {
		t.Fatalf("device = %+v, %v", device, ok)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run:

```powershell
go test ./internal/packagezip -run TestValidateRecognizesVehInteDevice -count=1
```

Expected: FAIL（`pkg.Devices` 中没有 `dc_VehInter_1`）。

- [ ] **Step 3: 实现前缀识别**

在 `internal/packagezip/business.go` 的 `inferDeviceType` 定义列表中增加：

```go
{"dc_VehInter_", cpdv1.DeviceType_DEVICE_TYPE_VEH_INTE},
```

- [ ] **Step 4: 实现节点引用解析**

在 `parseNode` 中，`LANMember.CCU` 解析之后、`Radio` 解析之前增加：

```go
vehIDs, err := optionalStringList(lanMember, "VehInter")
if err != nil {
	return nil, invalid("4_net_node/"+nodeID+".json", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, err)
}
if err := add(cpdv1.DeviceType_DEVICE_TYPE_VEH_INTE, "dc_VehInter_", vehIDs); err != nil {
	return nil, err
}
```

`parseDevice` 无需修改：`VehInte` 不落入无线电分支，返回基础 `Device`。

- [ ] **Step 5: 运行确认通过**

Run:

```powershell
go test ./internal/packagezip -run TestValidateRecognizesVehInteDevice -count=1
```

Expected: PASS。

---

## Task 5: 输出产物

**Files:**
- Create: `internal/output/vehinte.go`
- Modify: `internal/output/output.go`
- Test: `internal/output/output_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/output/output_test.go` 末尾增加：

```go
func TestProcessVehInteWritesTarAndUpdatesReReadINI(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ReReadJson.ini"), []byte("[Other]\nKeep=yes\n\n[ReReadVal]\nVal=0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pkg, packagePath := openTestPackage(t, vehInteEntries())
	defer pkg.Close()

	results, code := Process(context.Background(), Request{
		Root: root, Package: pkg, PackageName: filepath.Base(packagePath), NodeID: "nn_1",
		DeviceTypes: []cpdv1.DeviceType{cpdv1.DeviceType_DEVICE_TYPE_VEH_INTE},
		Bindings:    map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_VEH_INTE: "dc_VehInter_1"},
	})
	if code != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED || len(results) != 1 || results[0].Result != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("results = %v, code = %v", results, code)
	}

	names := readTarNames(t, filepath.Join(root, "vehinte.tar"))
	want := []string{
		"0_contacts/", "0_contacts/contacts_1.json",
		"3_device_config/", "3_device_config/dc_VehInter_1.json",
		"4_net_node/", "4_net_node/nn_1.json",
		"6_unit/", "6_unit/unit_1.json",
	}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("tar files = %v, want %v", names, want)
	}

	ini, err := os.ReadFile(filepath.Join(root, "ReReadJson.ini"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(ini)
	if !strings.Contains(text, "Keep=yes") || !strings.Contains(text, "[ReReadVal]\nVal=1") {
		t.Fatalf("INI = %q", text)
	}
}

func vehInteEntries() map[string][]byte {
	return map[string][]byte{
		"0_contacts/contacts_1.json":         []byte(`{"File":{"Guid":"c"},"UnitTree":{"SubUnits":["unit_1"],"NetNodes":["nn_1"]}}`),
		"3_device_config/dc_VehInter_1.json": []byte(`{"Model":"VehInte"}`),
		"4_net_node/nn_1.json":               []byte(`{"File":{"Guid":"n"},"SystemConfiguration":{"LANMember":{"VehInter":["dc_VehInter_1"]},"LANPrimary":{},"Radio":{}}}`),
		"6_unit/unit_1.json":                 []byte(`{"File":{"Guid":"u"},"UnitName":"Unit"}`),
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run:

```powershell
go test ./internal/output -run TestProcessVehInteWritesTarAndUpdatesReReadINI -count=1
```

Expected: FAIL（`unsupported device type`，且 `vehinte.tar` 未生成）。

- [ ] **Step 3: 实现 writeVehInte**

创建 `internal/output/vehinte.go`：

```go
package output

import (
	"context"
	"os"
	"path/filepath"

	"cpdc/internal/fsutil"
	"cpdc/internal/packagezip"
)

func writeVehInte(ctx context.Context, root string, pkg *packagezip.Package, device *packagezip.Device, nodeID string) error {
	files := filesWithPrefix(pkg.FileNames(), "0_contacts/", "6_unit/")
	files = append(files, device.Path, "4_net_node/"+nodeID+".json")
	files = uniqueSorted(files)

	tarTemp, err := createTar(ctx, root, pkg, files)
	if err != nil {
		return generationError(err)
	}
	defer os.Remove(tarTemp)

	if err := fsutil.AtomicCopy(tarTemp, filepath.Join(root, "vehinte.tar"), 0o600); err != nil {
		return writeError(err)
	}

	iniPath := filepath.Join(root, "ReReadJson.ini")
	existing, err := os.ReadFile(iniPath)
	if err != nil && !os.IsNotExist(err) {
		return generationError(err)
	}
	iniData := updateINI(existing, []iniUpdate{{section: "ReReadVal", key: "Val", value: "1"}})
	if err := fsutil.AtomicWriteFile(iniPath, iniData, 0o600); err != nil {
		return writeError(err)
	}
	return nil
}
```

- [ ] **Step 4: 接入 Process**

在 `internal/output/output.go` 的 `Process` switch 中，`default` 分支之前增加：

```go
case cpdv1.DeviceType_DEVICE_TYPE_VEH_INTE:
	err = writeVehInte(ctx, request.Root, request.Package, request.Package.Devices[deviceID], request.NodeID)
```

- [ ] **Step 5: 运行确认通过**

Run:

```powershell
go test ./internal/output -run TestProcessVehInteWritesTarAndUpdatesReReadINI -count=1
```

Expected: PASS。

---

## Task 6: 全量验证

- [ ] **Step 1: 格式化**

Run:

```powershell
gofmt -w internal\config\config.go internal\config\config_test.go internal\client\auth.go internal\client\auth_test.go internal\packagezip\business.go internal\packagezip\validate_test.go internal\output\output.go internal\output\output_test.go internal\output\vehinte.go
```

- [ ] **Step 2: 全量测试**

Run:

```powershell
go test ./...
```

Expected: PASS。

- [ ] **Step 3: vet**

Run:

```powershell
go vet ./...
```

Expected: PASS。
