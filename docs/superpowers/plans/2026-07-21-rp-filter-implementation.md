# CPDC rp_filter Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Linux CPDC disables reverse-path filtering for every active CPD broadcast interface and restores prior kernel values on graceful shutdown.

**Architecture:** A small platform-neutral controller owns sysctl state and rollback behavior. Linux supplies `/proc/sys` access, other operating systems supply a no-op controller, and UDP Transport invokes it before opening initial or refreshed broadcast senders.

**Tech Stack:** Go 1.25, standard library, build constraints, existing `internal/netio` tests.

---

### Task 1: Platform-neutral sysctl state controller

**Files:**
- Create: `internal/netio/rpfilter.go`
- Test: `internal/netio/rpfilter_test.go`

- [ ] Write tests using fake read/write functions for disabling `all` and selected interfaces, idempotent repeated calls, rollback after partial failure, and restoration.
- [ ] Run `go test ./internal/netio -run RPFilter -count=1` and verify the tests fail because the controller does not exist.
- [ ] Implement the minimal controller that records each original value once, writes `0`, rolls back only entries introduced by a failed call, and restores tracked values once.
- [ ] Re-run `go test ./internal/netio -run RPFilter -count=1` and verify it passes.

### Task 2: Platform adapters

**Files:**
- Create: `internal/netio/rpfilter_linux.go`
- Create: `internal/netio/rpfilter_other.go`

- [ ] Add a Linux factory backed by `/proc/sys/net/ipv4/conf/{all|interface}/rp_filter` using `os.ReadFile` and `os.WriteFile`.
- [ ] Add a `!linux` no-op implementation with the same interface.
- [ ] Cross-compile the package for Linux and Windows to verify build constraints and APIs.

### Task 3: UDP Transport lifecycle integration

**Files:**
- Modify: `internal/netio/udp.go`
- Modify: `internal/netio/udp_test.go`

- [ ] Write failing tests proving candidate preparation happens before refreshed senders open, preparation failure preserves the old pool, and `Close` invokes restoration.
- [ ] Run the focused Transport tests and verify expected failures.
- [ ] Add the controller to `Transport`; create and apply it after receive binding but before initial sender creation, apply it before refreshed sender creation, and restore it during `Close`.
- [ ] Re-run focused tests and all `internal/netio` tests.

### Task 4: Requirements and full verification

**Files:**
- Modify: `docs/requirements/02-CPDC-requirements.md`
- Modify: `docs/requirements/03-CPDS-CPDC-interaction-requirements.md`

- [ ] Document selected-interface-only behavior, the Linux `conf/all` interaction, dynamic interfaces, failure policy, graceful restoration, and non-Linux no-op behavior.
- [ ] Run `gofmt` on changed Go files.
- [ ] Run `go test ./...` and `go vet ./...`.
- [ ] Run `scripts/build.ps1` and verify every configured Windows/Linux target builds.
- [ ] Inspect `git diff --check` and `git status -sb` before handoff.
