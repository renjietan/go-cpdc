# CPDC Parse Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ensure CPDC does not report parse success until Linux has completed a filesystem-wide persistence barrier.

**Architecture:** Add a platform-specific `fsutil.Sync` function backed by `unix.Sync` on Linux and a no-op elsewhere. Inject it privately into `Parser`, then invoke it after output and snapshot commit but before returning success.

**Tech Stack:** Go, `golang.org/x/sys/unix`, Go build tags, standard `testing` package.

---

### Task 1: Prove the parse-success ordering

**Files:**
- Modify: `internal/client/parser_test.go`

- [x] **Step 1: Write the failing test**

Add `TestParserSyncsAfterOutputsAndSnapshot`, replace the parser's persistence callback, and assert from the callback that the CCU output and updated `cpdc_config.json` already exist.

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/client -run TestParserSyncsAfterOutputsAndSnapshot -count=1`

Expected: compilation failure because `Parser` does not yet expose the private persistence callback.

### Task 2: Add the persistence barrier

**Files:**
- Create: `internal/fsutil/sync_linux.go`
- Create: `internal/fsutil/sync_other.go`
- Modify: `internal/client/parser.go`

- [x] **Step 1: Write minimal platform implementation**

Define `fsutil.Sync()` as `unix.Sync()` on Linux and as a no-op under the complementary build tag.

- [x] **Step 2: Invoke it before parse success**

Store the callback on `Parser`, initialize it in `NewParser`, pass it into the parse operation, and call it immediately after `Config.UpdateSnapshot` succeeds.

- [x] **Step 3: Run focused test to verify it passes**

Run: `go test ./internal/client -run TestParserSyncsAfterOutputsAndSnapshot -count=1`

Expected: PASS.

### Task 3: Verify regression safety and target builds

**Files:**
- Verify only

- [x] **Step 1: Run all tests**

Run: `go test ./...`

Expected: all packages pass.

- [x] **Step 2: Compile platform targets**

Run: `$env:GOOS='linux'; $env:GOARCH='arm'; go build ./...` and `$env:GOOS='windows'; $env:GOARCH='amd64'; go build ./...`.

Expected: both commands compile successfully.
