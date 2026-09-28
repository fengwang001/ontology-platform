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

## 双流区间连接器（intervaljoin）

`intervaljoin` 把左右两条按事件时间非递减到达的事件流，按连接键与**闭合**事件
时间区间两两配对。代码见 `intervaljoin/`。

### 处理顺序（每来一个事件）

1. **校验**：侧合法、事件非 `nil`、`Lo <= Hi`、键非空、`Lo` 不低于本侧当前
   水位线、以及"提交 + 每步清理后"两侧保留事件数均不超上限；任一失败则整个
   操作被拒绝，**水位线、编号、保留状态、已输出对均不变**。
2. **更新水位线**：本侧水位线推进到事件的 `Lo`（允许相等，不允许倒退）。
3. **匹配**：与对侧同键的当前保留事件逐一判定闭合区间相交，命中即输出。
4. **入库**：新事件加入本侧保留集合。
5. **清理**：依据更新后的双侧水位线清理两侧已不可能再匹配的事件。

### 匹配条件（两端闭合）

`[aLo, aHi]` 与 `[bLo, bHi]` 匹配当且仅当同键且

```
aLo <= bHi 且 bLo <= aHi
```

- `[1,10]` 与 `[10,20]`：`10<=10`，在端点命中，**要配对**；
- `[1,10]` 与 `[11,20]`：`11>10`，相邻但不交，**不配对**；
- 左右事件最多配对一次（后到方扫描先到方的保留集合）。

### 清理规则（每步精确执行）

对侧未来事件的 `Lo` 不会低于其当前水位线 `wm`，因此一条 `[Lo,Hi]` 事件：

- `Hi < wm` → **remove**：与对侧未来事件永无交集；
- `Hi == wm` → **keep**：仍可能与下一条 `Lo == wm` 的事件在端点相交；
- 对侧水位线未建立（第一条事件到达前）→ 暂不清理。

每一步提交后左右两侧各清理一次；被清理事件的编号不回收。

### 拒绝原因（可区分）

通过 `errors.As` 取出 `*intervaljoin.JoinError`，按 `Code` 区分：

| Code | 触发场景 |
|---|---|
| `invalid_parameter` | 配置 `MaxRetainedPerSide<=0`、侧非法、事件 nil、`Lo>Hi` |
| `empty_key` | 连接键为空 |
| `time_regression` | 事件 `Lo` 低于本侧水位线 |
| `retention_limit_exceeded` | 一步提交并清理后某侧保留数超上限 |

### 并发与确定性

`Process` 由互斥锁串行化；`Pairs` / `Retained` / `Watermark` 在读锁内返回
**深拷贝快照**，所以并发只读看到的配对集合逐字段一致；同输入序列反复运行
（`TestDeterministicReplay`）输出完全相同。

### 日志

默认输出到 stderr（可由 `intervaljoin.SetLogger` 重定向，传 `nil` 丢弃）。
逐行记录：输入（侧、编号、键、区间、水位线变化）、每次匹配的两个子判定
（`Lo<=otherHi`、`otherLo<=Hi`）与布尔结果、每条事件清理的 `removed/kept`
判定依据、每步的输出对数与两侧保留数，以及被拒绝操作的 `reject` 行
（原因码与细节）。

### 本地验证

```bash
# 全量测试（含闭合边界、逐布清理精确性、各类非法输入、并发快照、确定性回放）
go test -race -v ./intervaljoin/

# 只跑边界与清理用例
go test -run 'TestClosedIntervalBoundaries|TestCleanupPrecisionPerStep' -v ./intervaljoin/

# 覆盖率
go test -coverprofile=coverage.out ./intervaljoin/
go tool cover -html=coverage.out

# 格式与静态检查
gofmt -l .
go vet ./...
```

