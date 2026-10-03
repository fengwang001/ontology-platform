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

## sequential：A/B 序贯检验停止判定器

`sequential` 包实现带样本比例校验、按有效检视次数换用边界表的序贯检验停止判定。
全部判定使用整数（`math/big`）运算，相同检视序列重放得到完全相同的结论序列。

### 接口

- `NewJudge(rA, rB, nmin, minStep, nmax, table, tf, tau)`：构造判定器，参数越界返回 `ErrInvalidParam`。
- `Look(nA, cA, nB, cB)`：提交两组累计样本数与累计转化数，返回结论（继续 / 胜出 / 变差 / 无效 / 比例异常）。
- `Status()`：返回状态、有效检视次数、上次计数 n 与最近一次结论。
- `Look` 与 `Status` 可并发调用，结果等价于某个串行顺序；停止后结论不可改变。

### 判定次序

每次 `Look` 先按序检查拒绝原因（只报第一个）：参数非法（`0 <= c <= n <= 1e6`）
→ 已停止 → 数据回退（任一数小于上一次被接受的检视）。被接受的检视（不论结论）
都会更新“上一次被接受”的基准值，然后按下列次序判定，命中即返回：

1. **比例异常**：`n >= 2*nmin` 且 `|nA*rB - nB*rA|*100 > tau*(nA*rB + nB*rA)`，停止，不计数。
2. **样本不足**：`nA < nmin` 或 `nB < nmin`，继续，不计数。
3. **仅观察**：`n - 上次计数 n < minStep`，继续，不计数（不推进边界序号）。
4. **有效检视**：第 `i` 次计数，更新上次计数 n，边界取 `T_min(i,k)`（序号超过 k 后沿用 `T_k`）。

### 统计量的整数化

令 `n = nA+nB`，`c = cA+cB`，`D = cB*nA - cA*nB`，则
`z^2 = D^2*n / (nA*nB*c*(n-c))`。为避免浮点误差，比较两边同乘分母与 100：

- 显著：`D^2*n*100 >= T * nA*nB*c*(n-c)`，`D>0` 胜出，`D<0` 变差，停止。
- 否则 `n >= nmax`：无效，停止。
- 否则 `2*n >= nmax` 且 `D^2*n*100 < Tf * nA*nB*c*(n-c)`：无效，停止。
- 否则继续。

乘积最大约 `2e32`，超过 int64，统一用 `math/big.Int` 比较。
`c` 为 0 或 `c == n` 时 `z^2` 未定义，按 `z^2 = 0` 处理：显著判定必为否；
无效边界的“小于”当且仅当 `Tf > 0` 时成立（此时 `D` 为 0，两端乘积均为 0，
不得用乘积形式直接比较）。

### 本地验证

```bash
# 单元测试（覆盖全部边界情形）+ 2000 组随机序列与朴素大整数模拟对照
go test ./sequential/ -v

# 竞态检测
go test ./sequential/ -race
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
