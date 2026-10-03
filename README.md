# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 重做日志缓冲（`redolog` 包）

`redolog` 实现带乱序拷贝完成、连续就绪水位与按块对齐刷盘的重做日志缓冲。
多个写入者先预留 LSN 区间、再以任意次序完成拷贝；所有方法均可并发调用，
结果等价于某个串行顺序。

### 预留、完成与就绪水位

- 状态：已预留水位 `R`、已落盘水位 `Fd`（初值均为起始 LSN `L0`），以及按
  预留次序排列的区间列表。
- `Reserve(n)` 预留区间 `[R, R+n)`：要求 `1 <= n <= Cap` 且
  `R+n-Fd <= Cap`（缓冲容量约束），成功后 `R` 前进 `n`。
- `Complete(start)` 将以 `start` 为起点的区间标记为已完成；重复完成或
  起点不存在会被拒绝且不改变任何状态。
- 就绪水位 `Ready`：从最早的区间起连续已完成的最长前缀中最后一个区间的
  终点（前缀为空时为 `L0`）。较晚的区间先完成不会推进 `Ready`；最早的
  区间完成后可一次越过多个已完成区间。任意时刻恒有
  `L0 <= Fd <= Ready <= R` 且 `R-Fd <= Cap`。

### 刷盘目标与写出块数

- `Flush(false)`：目标 `target = floor(Ready/B)*B`（向下取整到块边界，
  不足一块时不写）。
- `Flush(true)`：目标 `target = Ready`（强制写出残块）。
- `target <= Fd` 时不做任何事；否则若 `ceil(target/B)-floor(Fd/B) > Mx`，
  把 `target` 截为 `(floor(Fd/B)+Mx)*B`（必为块边界且大于 `Fd`，force
  同样受截断、不写残块）。
- 写出块数 `blocks = ceil(target/B) - floor(Fd/B)`（`Fd` 取刷盘前的值），
  之后 `Fd = target`。残块被续写后再次刷盘会按公式重复计入该块；
  每次 `blocks <= Mx`，总块数为全部有效刷盘的 `blocks` 之和。

### 等待者唤醒

- `Wait(w, end)`：`end` 必须是某个已预留区间的终点；若 `end <= Fd` 直接
  返回「已满足」且不登记，否则按递增登记序号登记等待者 `w`（`w` 非负、
  不可重复登记）。
- 每次刷盘后唤醒所有 `end <= Fd` 的等待者，按（`end` 升序，登记序号
  升序）返回其编号并移除；每个等待者至多被唤醒一次。

### 本地验证

```bash
# 单元测试 + 2000 组随机序列与朴素模拟的逐步对照（含竞态检测）
go test -race ./redolog/

# 查看随机对照日志（输入、输出与判定依据）
go test -run TestRandomAgainstNaive -v ./redolog/
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
