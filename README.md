# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## rollout：灰度分批发布评审器

`rollout` 包实现带基线对比闸门、通过连击与失败累计的分批发布评审器
（`Reviewer`）。所有操作与查询可并发调用，效果等价于某个串行顺序；
相同操作序列重放得到完全相同的返回与状态。

### 分批累计与去重

- 第 k 批累计实例数 `cum_k = ⌈N×p_k/100⌉`。
- 累计数与前一批相同的批被删去（保留先出现者），剩余批从 0 重新编号；
  因 `p_{m−1} = 100`，最后一批累计恰为 N。
- 任何时刻新版本实例数等于当前批（或回退后所在批）的 `cum`；
  完成时为 N，中止时为 0。

### 检视与闸门公式

- `Start(now)` 进入发布中，第 0 批开始时刻 `st = now`。
- `Observe(now, dRn, dEn, dRb, dEb)` 先累加四个增量：
  - `now < st + S`：浸泡中，不计检视；
  - `rn < Nmin`：样本不足，`ps`、`fs` 不变；
  - 否则做闸门判定：当 `en ≥ Ef` 且 `en×rb×100 > eb×rn×(100+tol)` 时判
    **失败**（乘积可达 10^21，按大整数/128 位精确比较；`rb = 0` 时左端
    为 0，必判通过），其余判 **通过**。

### 晋级、回退与冻结

- 通过：`ps++`；`ps` 达到 K 时晋级——最后一批则完成，否则进入下一批
  （`st = now`，四个累计值与 `ps`、`fs` 清零）。
- 失败：`fs++` 且 `ps = 0`；`fs` 达到 R 时回退——回退次数加一，第 0 批
  回退即中止（实例数 0），否则回到上一批（`st = now + H`，累计值与
  `ps`、`fs` 清零）；回退次数达到 M 且未中止时冻结（实例数为回退后所在
  批的 `cum`，不再接受 `Observe`；中止优先于冻结）。
- 晋级与回退除回退次数外不清除历史计数。

### 拒绝规则

拒绝原因可区分，按以下顺序只报第一个，且被拒绝的操作不改变任何累计
值、状态、批号与最大 `now`：

1. **参数非法**：构造参数越界、`now` 越界（合法范围 0..10^15）、增量越界
   或为负、增量中错误数大于请求数、累加后累计值超过 10^9；
2. **状态不符**：`Start` 时非未开始、`Observe` 时非发布中；
3. **时钟回退**：`now` 小于已被接受操作的最大 `now`（初值 0）。

### 本地验证

```bash
# 全部单测（边界、拒绝优先级、题目示例逐步复现）
go test ./rollout/

# 2000 组随机操作序列与朴素模拟对照，日志打印输入、输出与判定依据
go test -run TestRandomAgainstNaive -v ./rollout/

# 竞态检测
go test -race ./rollout/
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
