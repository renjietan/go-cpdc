# supportedTypes Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在CPDC配置示例中展示全部七种支持类型，同时保持运行时只使用 `deviceTypes`。

**Architecture:** 将 `supportedTypes`作为现有配置的未知扩展字段处理，复用已经过测试的未知字段保留能力，不增加业务状态或重复类型表。

**Tech Stack:** JSON、Markdown、Go现有测试套件

---

### Task 1: 示例与需求

**Files:**
- Modify: `cpdc_config.demo.json`
- Modify: `docs/examples/cpdc_config.demo.json`
- Modify: `docs/requirements/02-CPDC-requirements.md`
- Modify: `docs/requirements/03-CPDS-CPDC-interaction-requirements.md`
- Modify: `README.md`

- [x] **Step 1: Update both examples**

加入 `supportedTypes`，按固定顺序列出七种类型。

- [x] **Step 2: Document its informational semantics**

明确该字段不参与运行时选择和协议交互，实际身份只由 `deviceTypes`决定。

- [x] **Step 3: Run verification**

Run: `Get-Content -Raw cpdc_config.demo.json | ConvertFrom-Json; Get-Content -Raw docs/examples/cpdc_config.demo.json | ConvertFrom-Json; go test -count=1 ./...; go vet ./...`

Expected: 两份JSON有效，全部测试和静态检查成功。
