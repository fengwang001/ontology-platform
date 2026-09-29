# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 暂存提交器

`stagedcommit` 包提供带两阶段提交日志的键值暂存提交器。当前实现使用进程内状态模拟崩溃与恢复，不把日志落盘。

### 提交协议

1. `Put(key, value)` 只写入暂存区；重复写入同一个键会覆盖暂存值，暂存区在提交前对可见视图没有影响。
2. `Delete(key)` 只在键存在于当前可见视图、且该键尚未在本次暂存区被改动时允许。
3. `Commit()` 拒绝空暂存区；非空提交先分配从 1 递增的提交号，并写入状态为 `prepared` 的日志，然后再把整批变更应用到可见视图并把日志标记为 `committed`，最后清空暂存区。
4. `Rollback()` 无条件清空暂存区，不写日志，也不消耗提交号。
5. `ViewSnapshot()` 与 `StagedSnapshot()` 返回独立拷贝；读取通过读写锁观察完整的提交边界，调用方修改返回的 map 不会影响提交器。

### 崩溃与恢复

- `SimulateCrashAfterPrepare()` 复现崩溃恰好发生在 prepare 与定稿之间：日志保留一条 `prepared` 且未 `committed` 的条目，可见视图保持在上一个完整提交边界，暂存区清空，提交号已经消耗。
- `Recover()` 从日志尾部找到最新一条 `prepared` 条目，将其判定为 `aborted`（视为从未发生），并返回其提交号。
- 没有待判定条目时 `Recover()` 返回 0；恢复后再次调用也必定返回 0。被丢弃的提交号不会复用。

### 可判定错误

- `ErrEmptyKey`：空键。
- `ErrEmptyCommit`：暂存区为空时提交或模拟 prepare 后崩溃。
- `ErrDeleteNotAllowed`：删除目标不在可见视图中，或该键已经在当前暂存区被改动。

上述错误互不相同；错误返回前不会改变暂存区、视图、日志或提交号计数。

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

# 仅验证暂存提交器
go test -race -v ./stagedcommit

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
