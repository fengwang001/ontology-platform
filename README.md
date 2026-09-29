# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分布式追踪跨度组装器（`tracing` 包）

`tracing.Assembler` 把乱序到达的跨度组装成调用树，校正跨服务时钟偏移，
并在追踪完结后输出关键路径。输出与到达顺序无关，暂存跨度数受上限约束。

### 跨度模型

跨度 `Span` 含追踪号（`TraceID`）、跨度号（`SpanID`）、父跨度号
（`ParentID`，根为空）、服务名（`Service`）与起止时刻（`Start`/`End`）。

### 组装与完结规则

- 跨度通过 `AddSpan` 投递，时钟通过 `AdvanceClock(now)` 推进（注入时钟，
  只允许前进）；两者均可并发调用，内部由互斥锁串行化。
- 根跨度已到达、且注入时钟相对该追踪最近活动时刻（所有跨度的最大
  `End`）静默满时长 `T` 时，追踪完结并释放，输出 `TraceResult`。
- 完结时仍无父（父跨度号不存在）或父子成环的跨度列为孤儿（`Orphans`）。
- 以下跨度被拒绝并返回可区分的 `RejectReason`，且不改变任何状态：
  - `invalid_interval`：结束早于开始；
  - `self_parent`：以自己为父；
  - `duplicate_root`：同一追踪出现第二个根；
  - `conflicting_duplicate`：同一跨度号内容不同的重复；
  - `trace_finalized`：完结后到达的迟到跨度（单独计数，见 `LateCount`）；
  - `capacity_exceeded`：接受后未完结追踪的暂存跨度总数会超过上限。
- 内容相同的重复投递为幂等，不重复计数、不占容量。
- 恒有：已接受跨度数 = 已完结输出中的跨度数 + 未完结追踪的暂存跨度数。

### 偏移校正与重判顺序

- 自根向下逐层进行。子与父服务不同、且子区间不在父区间（父已平移后的
  区间）内时，把子跨度连同其子树中不经过其他服务的同服务后代整体平移。
- 平移量取使子区间落入父区间的最小绝对值：整体偏早则右移对齐父起点，
  整体偏晚则左移对齐父终点；子比父长时改为起点对齐。
- 异服务后代相对已平移的父重新判定，因此多层跨服务链会逐层累积平移量。
- 同服务后代不做判定，直接继承祖先的累计平移量。

### 关键路径

从根起，每层在子跨度中选（校正后）结束时刻最晚者，并列时取跨度号最小
者，直至叶子，构成 `CriticalPath`。

### 本地验证

```bash
# 全部测试（含全排列到达对比、并发与不变量检查）
go test ./tracing/

# 竞态检测 + 详细日志（打印输入、输出与判定依据）
go test -race -v ./tracing/
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
