# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 离群点摘除器（`ejector` 包）

`ejector` 实现带最大摘除比例、滑动结果窗口与乱序批量上报的主机离群点
摘除器。构造参数：主机数 `N`（编号 `0..N-1`）、连续失败阈值 `K`、基础
摘除时长 `B`、摘除时长上限 `Cap`、最大摘除百分比 `P`、结果窗口长度
`Wn`（1..64）、窗口失败率阈值 `Q`（1..100，百分数）；时刻与时长均为
`int64`。每台主机维护连续失败数 `c`、累计摘除次数 `e`、摘除截止时刻
`u` 与结果窗口 `win`（最近至多 `Wn` 次已记录上报，最旧在前）。

### 摘除条件（两路，任一满足即尝试摘除）

- **连续失败**：失败上报使 `c` 加一，当 `c >= K` 时触发。
- **窗口失败率**：仅当 `win` 长度恰等于 `Wn` 时评估，设窗口内失败数为
  `f`，当 `f*100 >= Q*Wn`（纯整数比较，不做除法取整）时触发；该条件可
  在 `c < K` 时单独生效。

成功上报使 `c` 清零、`e` 减一（`e` 为 0 时不变）并向 `win` 追加成功；
失败上报向 `win` 追加失败；`win` 超过 `Wn` 时丢弃最旧项。

### 比例上限与摘除时长

- 尝试摘除时设此刻被摘除主机数为 `E`，仅当 `(E+1)*100 <= P*N`（纯整数
  比较）时允许；即任意时刻被摘除主机数不超过 `⌊P*N/100⌋`。被上限挡下
  时 `c`、`e`、`u`、`win` 全部保留（含本次失败），此后每次失败都会重
  新尝试。`P=0` 永不摘除，`P=100` 总是允许。
- 摘除成功：`e` 加一，`u = now + min(Cap, B*e)`（用加一后的 `e`，随时
  间累计摘除次数线性加长、被 `Cap` 截断），随后 `c` 清零、`win` 清空。
- 恢复无需任何操作触发：`u > now` 时被摘除，恰在 `u` 时已恢复，恢复
  不改 `c`、`e`、`win`。被摘除期间的上报被忽略，不改变任何状态。

### 上报与查询

- `Report(host, ok, now)`：拒绝原因按顺序只报第一个——主机编号越界
  （`ErrHostOutOfRange`）、时间非法（`now` 不在 `[0, 1e15]`，
  `ErrInvalidTime`）、时钟回退（`now` 小于已接受的最大 `now`，初值 0，
  `ErrClockRegression`）。被拒绝的操作不改变任何状态；被忽略与被上限
  挡下都是被接受的上报，会推进最大 `now`。
- `ReportBatch(events)`：乱序批量上报，整批原子。先按输入顺序预检每
  个事件（先主机编号、再时间，报第一个有问题的事件），再检查批内最
  小 `now` 是否时钟回退；任一不通过则整批不改状态。全部通过后按 `now`
  升序稳定排序（`now` 相同保持输入顺序）逐条按 `Report` 规则处理，返
  回值按输入顺序排列，最大 `now` 推进到批内最大值。空批返回空列表。
- `Ejected(host, now)` / `Healthy(now)`：同样的拒绝检查（`Healthy` 无
  主机编号），但不推进最大 `now`。
- 所有方法可并发调用（内部互斥锁串行化），结果等价于某个串行顺序；
  相同上报序列重放得到完全相同的返回值与各主机状态。

### 本地验证

```bash
# 规则单元测试（含题目示例、阈值/窗口/比例边界、批原子性等）
go test ./ejector/

# 2000 组随机序列（含随机批）与朴素模拟对照，-v 打印输入、输出与判定依据
go test ./ejector/ -run TestRandomAgainstNaive -v

# 并发与竞态检测
go test -race ./ejector/
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
