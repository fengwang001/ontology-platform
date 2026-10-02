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

## 页分配器模型

根包提供 `PageAllocator`，按内存域空闲页、三档水位、跨域保留量和回收线程标志决定分配结果。所有数值都使用 64 位整数并向下取整。

### 水位推导

设域数为 `Z`、各域托管页数为 `M[z]`、全局最低空闲页数为 `totalMin`，且 `ΣM = ΣM[z]`：

- `min[z] = floor(totalMin × M[z] / ΣM)`
- `low[z] = min[z] + floor(min[z] / 4)`
- `high[z] = min[z] + floor(min[z] / 2)`
- `amin[z] = min[z] - floor(min[z] / 2)`
- `step[z] = max(1, floor(high[z] / 4))`
- `cap[z] = floor(high[z] / 2)`

保留量只对 `0 ≤ z < c < Z` 生效：

```text
reserve[z][c] = floor((M[z+1] + M[z+2] + … + M[c]) / r[z])
```

当 `c ≤ z` 时 `reserve[z][c] = 0`。

### 两遍检查与回退链

`Alloc(p, n, gfp)` 必须从单个域取走整数 `n` 页，回退链固定为：

```text
p, p-1, …, 0
```

对首选域 `p`、候选域 `z`，水位判定为严格不等式：

```text
F[z] - n > watermark[z] + reserve[z][p]
```

三种分配类别：

- `NORMAL`：第一遍用 `low[z] + boost[z]`；全部失败后唤醒 `kswapd[p]`，第二遍用 `min[z]`；仍失败返回 `ErrWouldReclaim`。
- `ATOMIC`：第一遍同 `NORMAL`；第二遍改用更深储备 `amin[z]`；仍失败返回 `ErrNoMemory`。
- `EMERGENCY`：不检查水位、保留量或回收标志，沿回退链选择第一个满足 `F[z] ≥ n` 的域；没有可用域时返回 `ErrNoMemory`。

两遍检查和实际扣页在同一互斥区内完成，因此对外等价于单个原子步骤。

### 回收线程与水位提升

- 第一遍失败且 `kswapd[p]` 原本为假时，置为真并让 `wakes++`；已经为真时不重复计数。
- `Free` 先增加 `F[z]`，再检查所有为真的 `kswapd[p]`；当 `F[p] > high[p] + boost[p]` 时才休眠并把 `boost[p]` 清 0。
- `SetTotalMin` 重算水位和步长后，先把每个 `boost[z]` 截断到新的 `cap[z]`，再执行同样的休眠判定；该操作只会清除，不会置位 `kswapd`。
- `NORMAL` 或 `ATOMIC` 在任一遍回退成功（实际域 `z != p`）时：

```text
boost[p] = min(cap[p], boost[p] + step[p])
```

- 分配到首选域和 `EMERGENCY` 分配都不会增加 `boost`。
- `kswapd[p]` 为假时，普通 `Free` 不会清除已有 `boost`。

### 查询与错误

查询包括 `FreePages(z)`、`Marks(z)`、`Boost(z)`、`Reserve(z,c)`、`Kswapd(p)`、`Stats()` 和非导出计数器观察方法 `Checks()`。

- 参数非法：构造器返回 `ErrInvalidConfig`；普通操作返回 `ErrInvalidArgument`，且整体拒绝、不修改状态。
- 释放超过托管容量：返回 `ErrOverfree`，且整体拒绝、不修改状态。
- 普通分配第二遍失败：返回 `ErrWouldReclaim`，只保留第一遍失败可能导致的 `kswapd` 与 `wakes` 变化。
- `ATOMIC` 与 `EMERGENCY` 无可用页：返回 `ErrNoMemory`。

### 本地验证

```bash
# 随机朴素模型对照与竞态检测
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache go test -race -v ./...

# 只运行 2000 组随机操作序列
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache go test -run TestRandomOperationsAgainstNaiveModel -v

# 覆盖率
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache go test -coverprofile=coverage.out ./...
```

随机测试中的朴素模拟器逐域线性扫描，并通过 `t.Logf` 记录输入、输出和判定依据；使用 `-v` 可查看完整日志。
