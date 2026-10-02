# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 成本分摊账本（`allocation` 包）

逐级下推（step-down）的内部服务部门成本分摊账本。`NewLedger(K, P)` 创建
K 个服务部门（编号 0..K−1）与 P 个生产部门（编号 K..K+P−1），`SetUsage(s, r, u)`
登记服务量（覆盖旧值、跨期保留），`Close(ds, dp)` 结账一期并返回按下推次序的
清单与各生产部门完全成本。所有方法可并发调用，效果等价于某个串行顺序。

### 下推次序：比例定义与逐步重算

每个结账期分 K 步。记 U 为尚未下推的服务部门集合。对 s∈U：

- `b_s` = `u[s][r]` 对「除 s 自身与已下推服务部门之外的所有部门 r」求和；
- `a_s` = 其中 r∈U 的部分之和。

每步选出 `a_s/b_s` 最大者下推；`b_s = 0` 视为比例最小；比较用整数交叉相乘
（`a_s×b_t` 与 `a_t×b_s`），相等取编号小者。**每一步都按当时的 U 与已下推集合
重新计算 a、b，不沿用上一步的值**——因此分母会随已下推部门被排除而缩小，
部门间的相对次序可能在步骤之间反转。

### 接收者与份额公式

被选中的 s 的待摊总额 `T_s = ds[s] + 本期此前各步分给 s 的份额`。接收者是
`u[s][r] > 0` 且 r 既不是 s 也不是已下推服务部门的全部部门，r 分得
`floor(T_s × u[s][r] / b_s)`（乘积可达 8×10¹⁹，实现用 128 位乘除，
`math/bits.Mul64` + `Div64`）。`T_s = 0` 时该步无分摊、视为已下推；
`T_s > 0` 而无接收者时整个 Close 以 `ErrNoRecipient` 拒绝。

### 余数按累计分摊额分配

余数 `m = T_s − 各份额之和`（`0 ≤ m < 接收者数`）。把接收者按
`(H_r 升序, 编号升序)` 排序，前 m 个各加 1 分。`H_r` 取本步开始时的值，
已含本期此前各步分给 r 的份额（每步结束立即计入 H），并跨期累计。
被拒绝的操作不改变服务量与 H：Close 在 H 的工作副本上推演，全部成功后才提交。

### 不变式

- 每步各接收者所得之和恰等于 `T_s`；
- 每期生产部门完全成本之和恰等于 `ds` 与 `dp` 之和；
- 任意时刻 `H_r ≥ 0`，且等于该部门历史上全部成功 Close 中所得份额之和；
- 相同操作序列重放得到完全相同的清单、完全成本与 H；
- 返回清单与 `History()` 均为新切片，与内部状态无别名。

### 本地验证

```bash
go test ./allocation/                 # 定向用例 + 2000 组随机序列对照大整数朴素模拟
go test -race -v ./allocation/        # 竞态检测；随机对照打印输入、输出与判定依据
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
