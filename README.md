# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 首写胜出寄存器（register 包）

`register` 包实现首写胜出（first-write-wins）的按键去重寄存器：每个键的生效值始终是迄今见过的**逻辑序号最小**的写入，因此生效序号随写入只减不增，结果对相同输入批次可复现。

### 写入规则

- 键尚无生效值：写入**建立**（`establish`）为该键生效值。
- 写入序号**更小**：先**撤回**（`retract`）当前已物化的那条（`key/seq/value` 必须恰好匹配），再建立新值；变更日志按 `retract` → `establish` 顺序记录。
- 写入序号**更大**：后写落败，写入被丢弃，计入累计丢弃数，不产生变更日志、不改变生效值。
- 写入序号与当前生效序号**重复**（含同一批次内先建立后又写同序号）：整批拒绝。

### 拒绝原因（可区分）

`Apply` 返回 `*RejectError`（可 `errors.As`），其 `Reason` 可 `errors.Is` 区分：

- `ErrEmptyKey`：键为空。
- `ErrNonPositiveSeq`：逻辑序号非正。
- `ErrSeqDuplicate`：同一键序号与当前生效序号重复。
- `ErrEmptyBatch`：批次为空。

批次为原子提交：先在工作副本上整批校验与模拟，**任一条被拒则整批不生效**，生效值与丢弃计数保持不变。

### 并发语义

`Get` / `Snapshot` / `Discarded` / `Check` 使用读锁，可与 `Apply` 及彼此并发；每次返回的都是逐字段复制的一致性快照，不暴露内部 map。`Check` 校验键非空、序号为正、键与条目匹配、丢弃计数非负。

### 本地验证方法

结果应等于“对全部已接受写入**逐键取最小序号**”。测试中的 `minSeq` 参照模型正是按此核对：

```bash
# 竞态检测 + 详细日志（含写入、变更日志、生效值、判定依据）
go test -race -v ./register

# 重复多轮以压低并发时序偶发问题
go test -race -count=10 ./register
```

手工核对步骤：

1. 列出所有成功批次中的写入，按 `key` 分组。
2. 每组取 `seq` 最小的一条（被拒批次不计入）。
3. 与 `Snapshot()` 的生效条目逐字段比较；后写落败条数应等于 `Discarded()`。
4. 变更日志中每次 `retract` 的 `key/seq/value` 必须恰好等于撤回前该键的生效条目。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
