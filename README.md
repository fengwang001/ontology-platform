# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## cgroup v2 风格内存保护计算器

`memoryprotect.Manager` 维护以 `/` 为根的分组树。只有叶子分组能通过 `SetUsage` 设置直接用量；非叶子用量是全部叶子后代用量之和。

有效 low 保护记为 `E`，从根向下推导。对非根分组 `c`，先取 `l_c = min(usage_c, low_c)`；当父分组 `p` 是根时 `E_c = l_c`。否则令 `S = Σ l_child`：

- `S ≤ E_p` 时，`E_c = l_c`；
- `S > E_p` 时，`E_c = floor(E_p · l_c / S)`。

有效 min 保护先以同样公式推导原始值 `raw_c`，其中使用 `m_c = min(usage_c, min_c)`、兄弟合计 `Sm` 和父分组 `Em_p`；最终 `Em_c = min(raw_c, E_c)`。比例乘法使用 Go `math/big.Int` 精确计算，可覆盖 10³⁰ 乘积并严格向下取整。

`Reclaim(need)` 在调用开始时一次性计算并冻结所有 `E` 与 `Em`，调用期间不重算：

1. 第一遍计算叶子的 `over1 = usage - E`，仅回收 `over1 > 0` 的叶子；按 `over1` 降序、路径字节序升序执行，每次回收 `min(剩余需求, over1)`。
2. 若第一遍后仍有剩余需求，第二遍基于第一遍结束后的当前用量计算 `over2 = currentUsage - Em`，按同样顺序回收。冻结的 `Em` 不因第一遍扣减而改变。
3. 同一叶子最多每遍出现一次；总回收量是 `min(need, 两遍可回收量之和)`。仍未满足需求时返回 `Insufficient=true`，这不是错误。

拒绝原因通过哨兵错误区分，并按参数非法、根不可操作、分组不存在、已存在、父不存在、父有用量、非叶子、有子分组的顺序报告第一个错误。被拒绝的操作不修改状态。所有公开方法使用同一互斥锁串行化，因此并发调用等价于某种合法串行顺序。

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

# cgroup 内存保护计算器：定点规则、2000 组朴素大整数模拟对照、竞态检测
go test ./memoryprotect -run 'TestSpec|TestEffective|TestHuge|TestReclaim|TestSecond|TestRejection' -v
go test ./memoryprotect -run TestRandomOperationsAgainstNaiveModel -v
go test ./memoryprotect -race -count=1

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
