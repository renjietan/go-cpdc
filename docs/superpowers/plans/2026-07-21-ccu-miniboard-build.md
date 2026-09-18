# CCU-Miniboard Build Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a repeatable build script that uses only the repository-local Go 1.23.12 toolchain to produce the Linux ARMv7 `CPDC-CCU-Miniboard` executable.

**Architecture:** A dedicated PowerShell entry point owns toolchain extraction, version validation, scoped cross-compilation environment variables, testing, and building. The existing release script remains unchanged. The module metadata is lowered to Go 1.23-compatible dependency requirements so the legacy-kernel toolchain can compile it.

**Tech Stack:** PowerShell, Go 1.23.12, Go modules, Go build metadata inspection

---

### Task 1: Add a failing MiniBoard build acceptance test

**Files:**
- Create: `tests/build_miniboard.test.ps1`

- [ ] **Step 1: Write the failing test**

Create a PowerShell test that invokes `scripts/build-miniboard.ps1`, requires `dist/CPDC-CCU-Miniboard`, and invokes the extracted `go.exe version -m` to assert `go1.23.12`, `GOOS=linux`, `GOARCH=arm`, `GOARM=7`, and `CGO_ENABLED=0`.

- [ ] **Step 2: Run test to verify it fails**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File tests/build_miniboard.test.ps1`

Expected: FAIL because `scripts/build-miniboard.ps1` does not exist.

- [ ] **Step 3: Commit the red acceptance test**

Run: `git add -- tests/build_miniboard.test.ps1 && git commit -m "Test CCU-Miniboard build workflow"`

### Task 2: Make module dependencies compatible with Go 1.23

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Select the local toolchain without extracting into the final directory**

Expand `compiler/go1.23.12.windows-amd64.zip` into the Go-ignored `compiler/.go1.23.12/` directory, verify its `go/bin/go.exe version`, and leave it intact for the script to reuse without deleting directories.

- [ ] **Step 2: Lower module requirements**

Use the Go 1.23.12 executable with `GOTOOLCHAIN=local` to set the main module directive to `go 1.23.0` and select `golang.org/x/sys@v0.30.0`, whose APIs cover the project's `unix.Statfs` usage and whose module supports older Go versions. Run `go mod tidy` with the same executable.

- [ ] **Step 3: Verify dependency resolution and tests**

Run with local Go 1.23.12: `go test ./...`

Expected: all packages pass with no toolchain upgrade.

- [ ] **Step 4: Commit module compatibility changes**

Run: `git add -- go.mod go.sum && git commit -m "Support building CPDC with Go 1.23"`

### Task 3: Implement the dedicated MiniBoard script

**Files:**
- Create: `scripts/build-miniboard.ps1`
- Modify: `.gitignore`

- [ ] **Step 1: Implement minimal toolchain preparation**

Resolve paths relative to the script, require `compiler/go1.23.12.windows-amd64.zip`, expand it only when the dedicated directory is absent, validate `go.exe`, and require exact `go version go1.23.12 windows/amd64` output. Never delete a directory when recovery is needed.

- [ ] **Step 2: Implement scoped test and build**

Save and restore `GOOS`, `GOARCH`, `GOARM`, `CGO_ENABLED`, and `GOTOOLCHAIN` in `try/finally`. Set `GOTOOLCHAIN=local`, run host-compatible `go test ./...` first, then set `GOOS=linux`, `GOARCH=arm`, `GOARM=7`, `CGO_ENABLED=0`, and run `go build -trimpath -ldflags "-s -w" -o dist/CPDC-CCU-Miniboard ./cmd/cpdc` without a `buildinfo.Target` override.

- [ ] **Step 3: Ignore the expanded local compiler**

Add `/compiler/.go1.23.12/` to `.gitignore` while leaving the user-provided ZIP outside the implementation commit.

- [ ] **Step 4: Run the acceptance test to verify it passes**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File tests/build_miniboard.test.ps1`

Expected: PASS and prints the verified output path and build metadata.

- [ ] **Step 5: Commit the implementation**

Run: `git add -- scripts/build-miniboard.ps1 .gitignore && git commit -m "Build CCU-Miniboard with local Go 1.23"`

### Task 4: Final verification

**Files:**
- Verify: `dist/CPDC-CCU-Miniboard`

- [ ] **Step 1: Run all Go tests with local Go 1.23.12**

Run the local `go.exe test ./...` with `GOTOOLCHAIN=local` and a host build environment.

- [ ] **Step 2: Re-run the build script from a clean process environment**

Run: `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-miniboard.ps1`

Expected: exit code 0 and `dist/CPDC-CCU-Miniboard` is refreshed.

- [ ] **Step 3: Inspect the executable**

Run: local `go.exe version -m dist/CPDC-CCU-Miniboard`

Expected metadata: `go1.23.12`, `GOOS=linux`, `GOARCH=arm`, `GOARM=7`, `CGO_ENABLED=0`; no `-X cpdc/internal/buildinfo.Target=ccu` linker setting.

- [ ] **Step 4: Check repository state**

Run: `git diff --check` and `git status --short`.

Expected: no whitespace errors; only the user-provided compiler archive/directory and generated ignored artifacts remain outside committed source changes.
