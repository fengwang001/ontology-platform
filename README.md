# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 大事务溢写与按序回放（`spill` 包）

`spill.Manager` 在内存缓冲有上限时，把未结束事务的行整体溢写到溢写块存储；
提交时按追加顺序读回输出，回滚则全部丢弃。下游日志只包含已提交事务的行。

### 溢写规则

- `Config.MemRowLimit` 是所有未结束事务**内存行数总和**的硬上限（`>=1`）。
- 每次 `Append` 后，若内存总行数超过上限，就从**未结束事务**中选出
  内存行数最多者；并列时取**事务号最小**者，把它当前全部内存行打包成一个
  溢写块并清空其内存；如此反复，直到内存总行数不超过上限。
- 块号由单调递增计数器分配，**永不复用**（提交/回滚删除块后也不回退）。
- `Config.BlockLimit` 是溢写块总数上限（`>=1`，按“当前已占用块数”计）。
  若本次追加所需的新块数会让块数超过上限，则**本次追加整体被拒**
  （`ReasonSpillStorageFull`）：先在临时计数上 dry-run 出全部溢写计划，
  容量不足时一行都不写入、一个块都不创建。

### 提交回放

- `Commit(txnID)`：先按**块号升序**（块号即溢写发生顺序）依次回放该事务的
  全部溢写块，再输出其仍在内存中的行；输出行序与该事务的追加顺序完全一致。
- 回放后删除该事务的全部块并移除事务，输出行追加进下游已提交日志
  （`CommittedLog()`）。下游因此只会看到已提交事务。

### 回滚

- `Rollback(txnID)`：丢弃内存行、删除全部溢写块、移除事务，不输出任何行，
  也不写入下游日志。

### 边界与错误类别

所有错误都返回 `*spill.Error`，其 `Reason` 字段互不相同、可区分：

- `ReasonInvalidArgument`：上限 `<1`、事务号为 0、追加空批次或空行。
- `ReasonTxnDuplicate`：`Begin` 了一个仍未结束的同号事务。
- `ReasonTxnNotFound`：向不存在（或已结束）的事务追加/提交/回滚。
- `ReasonSpillStorageFull`：本次追加所需溢写块数会突破块上限。

任何一次被拒的调用都是**原子**的：内存行、溢写块、事务表、已提交日志、
块号计数器均保持调用前状态，失败不留痕（块号仅在真正创建块时才递增）。

### 并发与自检

- 内部使用读写锁：`CommittedLog()`、`Blocks()`、`Stats()` 取读锁，
  可被多个执行体并发调用，且可与 `Append`/`Begin`/`Commit`/`Rollback` 并发。
- `CheckInvariants()` 随时可调用，校验：
  - 内存行数总和不超过 `MemRowLimit`；
  - 每个溢写块都恰好属于一个未结束事务，事务引用的块都存在。
- 传入 `Logger`（可用并发安全的 `spill.NewEventLog()`）即可记录每一步的
  操作、事务号、输入行数、当时内存行数与判定依据（含选中受害者的理由、
  存储满拒绝依据等）。

### 本地验证

```bash
# 单元测试（含与无上限朴素参照的一致性、并发竞态检测）
go test -race -v ./spill

# 全量
go test -race ./...

# 自检工具
gofmt -l .
go vet ./...
```

测试覆盖：溢写对象选择（最多内存行、并列取最小事务号）、提交跨块与内存的
回放顺序、回滚清理、非法参数/不存在/重复事务/存储满四类错误、拒绝后
内存/块/下游日志/统计完全不变，以及与无上限朴素参照的逐事务输出一致性。

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

## 代码检查

```bash
gofmt -l .
go vet ./...
```
