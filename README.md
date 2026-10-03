# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## pane 包：滑动窗口窗格的输出时间戳合并与水位保持器

`pane` 包（`pane/pane.go`）把元素分配到多个重叠滑动窗口，按输出时间戳
策略为每个窗格维护保持值 hold，并据此推导只进不退的输出水位线 O。

### 窗口分配与取整

- 窗口为 `[ws, ws+S)`，起点 `ws = k×D`（k 为整数，ws 可为负），`end = ws+S`。
- 元素 `ts` 属于所有满足 `ws <= ts < ws+S` 的窗口，即
  `k` 从 `ceil((ts-S+1)/D)` 到 `floor(ts/D)`。
- 取整必须按数学定义（对负数同样成立）。Go 的 `/` 是向零截断，
  直接用于负数的 floor 会漏窗口（如 `floor(-7/5)` 应为 -2 而非 -1），
  包内用 `floorDiv`/`ceilDiv` 实现数学取整。

### 输出时间戳策略与保持值

每个缓冲窗格的原始输出时间戳 raw：

- `Earliest`：窗格内最小元素 ts；
- `Latest`：窗格内最大元素 ts（随更大 ts 的元素到来而上升）；
- `End`：窗口结束 `end-1`（不随元素变化）。

保持值 `hold = max(raw, O)`：输出水位 O 只进不退，任何窗格的输出时间戳
都不得小于当前 O，因此 raw 小于 O 时被钳制到 O。由此推出
`O = max(O, min(I', 剩余窗格 hold 的最小值))`（无剩余窗格则取 I'），
保证 `O <= I` 且每个发出的 ON_TIME 窗格输出时间戳不小于发出前的 O。

### 迟到与丢弃判据（按窗口分别判定）

设当前输入水位为 I，窗口结束为 end：

- `I < end`：进入缓冲（占用窗格表容量）；
- `end <= I < end+AL`：立即发出迟到窗格 `(ws, 1, val, max(f, O))`，
  其中 `f` 对 EARLIEST/LATEST 取元素 ts，对 END 取 `end-1`；
- `I >= end+AL`：丢弃，丢弃计数按窗口加一。

迟到与丢弃是正常结果而非拒绝，也不受容量限制；但若缓冲类窗口所需新
窗格数加窗格表现有大小超过 Cap，整个 Add 被拒绝（不发出迟到窗格、
不计丢弃、不改变任何状态）。

### 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素模拟对照、并发不变量检查）
go test ./pane/

# 查看随机对照日志（输入、输出与逐窗口判定依据）
go test ./pane/ -run 'TestRandomAgainstNaive' -v

# 竞态检测
go test -race ./pane/
```

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
