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

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 事务重组组件（`transaction` 包）

把交错到达的变更事务按事务边界重组，使下游以事务为单位**原子地**收到数据。

### 事务生命周期

每个事务经历完整生命周期，由四个事件驱动（方法或 `Apply(Event)` 两种入口等价）：

```
Begin(txID) ──► Write(txID, row)* ──┬─► Commit(txID)   整体输出，缓冲归零
                                    └─► Rollback(txID) 丢弃全部行，缓冲归零
```

- `Begin` 开启事务；事务标识非空，且同名事务不能重复开始。
- `Write` 按调用（到达）顺序把行追加进该事务的缓冲。
- `Commit` 提交：**立即**按行的到达顺序输出该事务的完整行集；**空事务也会输出**
  （`Rows` 为长度 0 的非 nil 切片）。
- `Rollback` 回滚：丢弃该事务缓冲的全部行，不产生任何输出。
- 提交或回滚后事务即结束，再次写/提交/回滚都按“事务不在进行中”拒绝。

### 输出顺序

- 输出顺序**即提交事件的到达顺序**，与各事务开始/写入的先后无关。
- 每次提交返回的 `CommittedTx` 带单调递增的提交序号 `Seq`（从 1 开始），
  可用于下游去重/排序；`Rows` 是内部缓冲的独立拷贝，调用方修改不影响重组器。

### 有界缓冲与拒绝原因

所有**进行中事务**的缓冲行总数受 `NewReassembler(maxBufferedRows, ...)` 上限约束
（全局共享，而非每事务）。一次 `Write` 会令总行数超限即被拒绝，且该行不进入缓冲。
所有非法操作都返回携带可区分原因的 `*RejectError`（可用 `errors.Is` 命中对应哨兵），
被拒绝的操作**不改变**事务状态、缓冲、已输出事务或提交序号：

| 原因（`RejectReason`） | 哨兵错误 | 触发条件 |
| --- | --- | --- |
| `invalid_event` | `ErrInvalidEvent` | `Apply` 收到未知事件类型 |
| `empty_tx_id` | `ErrEmptyTxID` | 事务标识为空 |
| `duplicate_begin` | `ErrDuplicateBegin` | 同名事务已在进行中又 Begin |
| `tx_not_found` | `ErrTxNotFound` | 对未开始/已结束事务 Write/Commit/Rollback |
| `buffer_limit_exceeded` | `ErrBufferLimitExceeded` | Write 会使全局缓冲行数超过上限 |

### 并发与确定性

`Reassembler` 的所有方法均可并发调用，内部用单一互斥锁把
“校验 → 修改缓冲 → 生成输出/记账”作为临界区原子完成。因此：

- 提交与回滚可并发发起；每个事务发出的行与其缓冲行**逐条一致**，结束后缓冲份额立即释放，
  最终全部归零时 `BufferedRows()==0`、`InFlight()==0`。
- **同一输入序列反复计算得到完全相同的输出**（事务、序号、行顺序都一致），
  与 goroutine 调度无关。

### 判定日志

传入 `transaction.WithLogger(w)` 后，每次操作都会写一行：

- `in  ...`：输入事件（含行内容）；
- `ok  ...`：接受但无输出（Begin/Write/Rollback），附 `buffered`/`inflight` 判定后状态；
- `out COMMIT ...`：提交输出，含 `seq`、行列表与判定后状态；
- `rej ... reason=...`：拒绝及其原因。

### 本地验证

```bash
# 单元测试（务必带 -race 验证并发提交/回滚）
go test -race -v ./transaction/

# 全量测试 + 覆盖率
go test -race ./...
go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out

# 演示程序：终端直接运行（无管道输入）会跑内置交错事务示例
go run ./cmd/txdemo

# 用自定义事件脚本驱动（# 开头为注释）
#   BEGIN <txID> / WRITE <txID> <key> <value> / COMMIT <txID> / ROLLBACK <txID>
printf 'BEGIN a\nWRITE a k v\nCOMMIT a\n' | go run ./cmd/txdemo

# 收紧全局缓冲上限以观察 buffer_limit_exceeded 拒绝
go run ./cmd/txdemo -limit 2 <<'EOF'
BEGIN a
BEGIN b
WRITE a 1 one
WRITE b 2 two
WRITE a 3 three
COMMIT a
COMMIT b
EOF
```

