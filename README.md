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

## 重做日志缓冲（`redolog` 包）

`redolog.Buffer` 支持多个写入者先预留 LSN 区间、再以任意次序完成拷贝，
由连续完成的前缀决定可落盘上界，并按块对齐规则刷盘、唤醒提交等待者。
所有方法可并发调用，效果等价于某个串行顺序。

### 预留、完成与就绪水位

- 构造参数：起始 LSN `L0`、块大小 `B`、缓冲容量 `Cap`、单次刷盘块数上限 `Mx`。
- `Reserve(n)` 预留区间 `[R, R+n)`，要求 `R+n-Fd <= Cap`，成功后 `R` 前进。
- `Complete(start)` 把以 `start` 起点的区间标记为已完成。
- 就绪水位 `Ready`：从最早预留的区间起，连续已完成的最长前缀中最后一个
  区间的终点；前缀为空时为 `L0`。较晚区间先完成不会推进 `Ready`，最早
  区间完成后会一次越过整段已完成前缀。
- 任意时刻满足 `L0 <= Fd <= Ready <= R` 且 `R-Fd <= Cap`，`Ready` 与 `Fd`
  只增不减。

### 刷盘目标与写出块数

- `Flush(false)`：目标 `target = floor(Ready/B)*B`（向下取整到块边界，
  不足一块时什么都不写）。
- `Flush(true)`：目标 `target = Ready`（写出残块）。
- `target <= Fd` 时不做任何事；否则若所需块数超过 `Mx`，把 `target` 截为
  `(floor(Fd/B)+Mx)*B`（必为块边界，`force` 同样受截断，不写残块）。
- 写出块数 `blocks = ceil(target/B) - floor(Fd/B)`，之后 `Fd = target`。
  残块被续写后再次刷盘会按该公式重复计入该块；总块数等于全部有效
  `Flush` 的 `blocks` 之和，且每次 `blocks <= Mx`。

### 等待者唤醒

- `Wait(w, end)`：`end` 必须是某个已预留区间的终点；若 `end <= Fd` 直接
  返回「已满足」且不登记，否则按递增登记序号登记为等待者。
- 每次有效刷盘后，唤醒所有 `end <= Fd` 的等待者，按（`end` 升序，登记
  序号升序）返回编号并移除；每个等待者至多被唤醒一次。

### 本地验证

```bash
# 全部规则单测 + 2000 组随机序列与朴素模拟对照
go test ./redolog

# 查看随机对照日志（输入、输出与判定依据）
go test ./redolog -run TestRandomAgainstSimulation -v

# 竞态检测（并发等价串行）
go test -race ./redolog
```
