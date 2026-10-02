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

## 页框分配器模型

页框分配器位于根包 `pagealloc`，入口为 `New(zones, managed, ratios, totalMin)`。页数与水位均使用 `int64`；`totalMin × M[z]` 最大为 `4×10^18`，在 `int64` 范围内。

### 水位推导

设 `S = Σ M[z]`，所有除法均为整数向下取整：

- `min[z] = floor(totalMin × M[z] / S)`
- `low[z] = min[z] + floor(min[z] / 4)`
- `high[z] = min[z] + floor(min[z] / 2)`
- `amin[z] = min[z] - floor(min[z] / 2)`
- `step[z] = max(1, floor(high[z] / 4))`
- `cap[z] = floor(high[z] / 2)`

首选域为 `c`、候选域为 `z` 时，跨域保留量为：

- `z < c`：`reserve[z][c] = floor((M[z+1] + … + M[c]) / r[z])`
- `z >= c`：`reserve[z][c] = 0`

水位检查谓词为严格不等式：

```text
F[z] - n > mark + reserve[z][c]
```

### 分配、回退与回收线程

`Alloc(p, n, gfp)` 只从单个域一次性取 `n` 页，回退链固定为 `p, p-1, …, 0`。

- `NORMAL`：第一遍使用 `low[z] + boost[z]`；失败后唤醒 `kswapd[p]`，第二遍使用 `min[z]`；仍失败返回 `ErrWouldReclaim`。
- `ATOMIC`：第一遍同 `NORMAL`；失败后唤醒 `kswapd[p]`，第二遍使用更深储备 `amin[z]`；仍失败返回 `ErrNoMemory`。
- `EMERGENCY`：不做水位和保留量检查，沿回退链选择第一个满足 `F[z] >= n` 的域；失败返回 `ErrNoMemory`，且不修改任何 `kswapd` 标志。

第一遍全部失败时，若 `kswapd[p]` 原本为假，则置真且 `wakes++`；已为真时不重复计数。两次线性扫描在同一临界区内完成，因此“检查并扣减页框”是原子步骤，单次 `Alloc` 的判定次数不超过 `2×Z`，可通过非导出计数器 `checks` 验证。

`NORMAL` 或 `ATOMIC` 在任一遍回退到非首选域时：

```text
boost[p] = min(cap[p], boost[p] + step[p])
```

分到首选域不增加 `boost`；`EMERGENCY` 即使实际域不同，也只计入回退统计，不增加 `boost`。

### 释放、休眠与动态最低水位

`Free(z, n)` 先检查 `F[z] + n <= M[z]`，超过托管量返回 `ErrOverfree` 且不改变状态。成功释放后，对所有为真的 `kswapd[p]` 执行：

```text
if F[p] > high[p] + boost[p] {
    kswapd[p] = false
    boost[p] = 0
}
```

比较是严格大于：恰好等于 `high[p] + boost[p]` 时不休眠。`kswapd[p]` 为假时不清零已有 `boost`，所以无回收活动期间保留的水位提升会继续影响第一遍。

`SetTotalMin(v)` 只接受 `0 <= v <= S`，随后重算除 `reserve` 外的全部推导量，先把每个 `boost[z]` 截到新的 `cap[z]`，再执行与 `Free` 相同的休眠判定。该操作只会清除 `kswapd`，不会置位。

### 查询、错误与并发

- 查询：`FreePages(z)`、`Marks(z)`、`Boost(z)`、`Reserve(z,c)`、`Kswapd(p)`、`Stats()`。
- 参数非法统一返回 `ErrInvalidArgument`，拒绝操作不改变任何状态。
- `ErrWouldReclaim` 与 `ErrNoMemory` 是执行失败；除第一遍失败可能设置 `kswapd[p]` 与增加 `wakes` 外，不修改空闲页、boost 或成功统计。
- 所有操作由同一把读写锁保护，并发调用等价于某个合法串行顺序。

### 本地验证

```bash
go test ./...
go test -race ./...
go test -run TestRandomSequencesAgainstNaiveModel -v ./...
```

随机测试使用 2000 组固定种子操作序列，将实现与逐域线性扫描的朴素模型逐步对照，并记录每次操作的输入、返回值、扫描水位或回收判定依据。

## 常规测试

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
