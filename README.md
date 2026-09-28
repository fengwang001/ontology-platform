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

## 事件时间去重窗口（`dedup` 包）

`dedup` 包提供基于**事件时间（event time）**的去重组件：按事件标识去重，
去重记忆随事件时间水位线过期清除，使重复判定与记忆内存在任意事件到达顺序下
（乱序、迟到、重复）都正确。

### 规则

1. **水位线（watermark）**：取所有已处理事件时间的最大值，单调不减。
   迟到事件（事件时间早于水位线）不会使水位线回退，也**不会仅因迟到被丢弃**。
2. **记忆**：每个标识只记忆“它第一次被判为新事件那条事件”的事件时间。
   重复事件只丢弃并计数，**不刷新**记忆时间。
3. **过期**：当 `记忆时间 + TTL <= 水位线` 时，该记忆立即被清除。
   边界是闭区间——水位线**恰好**到达 `记忆时间 + TTL` 的那一刻即清除。
   过期清除发生在每次处理事件、推进水位线时。
4. **判定**：
   - 标识在（候选水位线下）未过期的记忆中存在 → 重复，丢弃，`DuplicateCount++`。
   - 否则 → 新事件，接受输出，`AcceptedCount++`，并以本事件时间建立记忆。
   - 迟到过久的新事件（其 `事件时间 + TTL` 已不晚于水位线，即“出生即过期”）
     仍作为新事件输出，但不保留记忆、不占用记忆条数。
5. **记忆上限**：`MaxEntries` 限制过期清除后仍保留的记忆条数。
   接受新事件会超限（且该事件的记忆需要保留）时，整个输入被拒绝，
   **不推进水位线、不清除记忆、不变更任何计数**。重复事件不产生新记忆，
   因此在记忆满时仍正常判重。
6. **拒绝原因可区分**：构造参数非法、空标识、零事件时间、记忆超限分别返回
   `ErrInvalidParameter` / `ErrEmptyID` / `ErrInvalidTime` / `ErrMemoryLimit`
   （可用 `errors.Is` 判定，或从 `*RejectError` 的 `Reason` 取常量）。
   被拒绝的输入不改变水位线、记忆、重复计数或已输出事件。
7. **并发与确定性**：`Process` / `Snapshot` 由互斥锁保护；`Snapshot` 返回
   字段间一致的深拷贝快照。相同输入序列单线程复算得到完全相同的输出，
   过期列表按（记忆时间, 标识）排序以保证确定性。

### 日志

每次处理打印：输入（`event received`：id、event_time）、新事件
（`event accepted as new`）或重复（`duplicate discarded`），以及判定依据
`basis`（`no live memory for id` / `id already remembered`）、
`first_seen`、`watermark`、`remembered_until` 和本次清除条数；拒绝时打印
`reason`。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 详细输出（并发快照一致性在此模式下验证）
go test -race -v ./dedup/

# 运行演示：观察输入、新/重复判定、判定依据与边界过期日志
go run ./cmd/dedupdemo

# 覆盖率
go test -coverprofile=coverage.out ./dedup/
go tool cover -html=coverage.out
```

