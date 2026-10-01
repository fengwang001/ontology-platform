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

## writestall：带迟滞的 LSM 写停顿控制器

`writestall` 包依据零层文件数 `n0`、待压实字节 `pend`、冻结内存表数 `imm`
在 **正常（Normal）/ 减速（Slowdown）/ 停写（Stopped）** 三态间迁移，并给出减速延迟。

### 参数

- `S1 < S2`：零层文件数减速 / 停写阈值
- `P1 < P2`：待压实字节减速 / 停写阈值
- `I > 0`：冻结内存表数停写阈值
- `D > 0`：最大延迟（微秒，正整数）

构造参数按 S、P、I、D 顺序校验，只报第一个错误（`ErrSlowdownThresholdOrder`、
`ErrPendingThresholdOrder`、`ErrImmThreshold`、`ErrMaxDelay`）。
`Observe` 的负参数按 `n0`、`pend`、`imm` 顺序拒绝
（`ErrNegativeL0`、`ErrNegativePending`、`ErrNegativeImm`）；
`Admit` 在停写时返回与参数错误可区分的 `ErrWriteStopped`。
被拒绝的操作不改变状态。

### 判定顺序（每次 Observe）

1. 任一停写条件（`n0 ≥ S2`、`pend ≥ P2`、`imm ≥ I`）成立 → 停写；
2. 否则若当前为停写：仅当三者分别**严格小于**各自恢复线
   `⌊3S2/4⌋`、`⌊3P2/4⌋`、`⌊3I/4⌋` 才离开，离开后按减速条件判为减速或正常，
   否则保持停写；
3. 否则若减速条件（`n0 ≥ S1` 或 `pend ≥ P1`）成立 → 减速；
4. 否则若当前为减速：仅当 `n0 < ⌊3S1/4⌋` 且 `pend < ⌊3P1/4⌋` 才回到正常，
   否则保持减速；
5. 否则正常。

### 延迟公式

减速态下 `delay = max(1, ⌈D·ρ⌉)`，其中
`ρ = clamp(max((n0−S1)/(S2−S1), (pend−P1)/(P2−P1)), 0, 1)`，
全程使用整数有理数计算；其他状态延迟为 0。

### 并发与可复现性

所有方法由互斥锁保护，并发调用等价于某个串行顺序；
相同观测序列重放得到完全相同的状态与延迟序列。

### 本地验证

```bash
# 全部单测（含阈值/恢复线恰等于与差 1、全部三态迁移、延迟边界）
go test ./writestall/

# 对拍日志：3000 步随机观测 vs 朴素状态机，打印输入、输出与判定依据
go test -run TestFuzzAgainstNaive -v ./writestall/

# 竞态检测（含并发等价性测试）
go test -race ./writestall/
```
