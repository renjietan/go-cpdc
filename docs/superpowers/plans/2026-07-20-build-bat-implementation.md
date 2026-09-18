# CPDC Build BAT Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `scripts/`下增加可双击调用现有PowerShell构建的 `build.bat`。

**Architecture:** BAT只负责定位和启动同目录 `build.ps1`、保留退出码并暂停窗口；所有构建逻辑继续由PowerShell脚本维护。路径使用 `%~dp0`，不依赖当前工作目录。

**Tech Stack:** Windows Batch、Windows PowerShell、现有Go构建脚本

---

### Task 1: Add the double-click wrapper

**Files:**
- Create: `scripts/build.bat`
- Modify: `README.md`

- [x] **Step 1: Verify the wrapper is absent**

Run:

```powershell
if (-not (Test-Path .\scripts\build.bat)) { throw 'scripts/build.bat is absent' }
```

Expected: FAIL with `scripts/build.bat is absent`。

- [x] **Step 2: Create the wrapper**

Create `scripts/build.bat` with:

```bat
@echo off
setlocal

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1"
set "BUILD_EXIT_CODE=%ERRORLEVEL%"

echo.
if not "%BUILD_EXIT_CODE%"=="0" (
    echo Build failed with exit code %BUILD_EXIT_CODE%.
    pause
    exit /b %BUILD_EXIT_CODE%
)

echo Build completed successfully.
pause
exit /b 0
```

Add the `scripts\build.bat` double-click option next to the existing PowerShell command in README.

- [x] **Step 3: Verify the wrapper from a different working directory**

Run:

```powershell
cmd.exe /d /c "cd /d %TEMP% && echo.|call D:\CPD\CPDC\scripts\build.bat"
if ($LASTEXITCODE -ne 0) { throw "build.bat failed with $LASTEXITCODE" }
```

Expected: PASS with `Build completed successfully.`，并由PowerShell脚本在项目 `dist/`生成五个目标。

- [x] **Step 4: Run regression checks**

Run:

```powershell
go test -count=1 ./...
go vet ./...
git diff --check
```

Expected: 全部成功。
