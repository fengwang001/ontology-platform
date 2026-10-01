# 代价感知构建制品缓存（GDSF）

`ontology` 包提供字节容量受限的构建制品缓存
（`ontology/gdsf.go`），按 **Greedy-Dual-Size-Frequency** 策略驱逐。
所有优先级与通胀值都用 `math/big.Rat` 精确表示，**全程无浮点**，
因此相同操作序列在任何机器上重放都得到完全相同的驱逐序列与 `L`。

## 优先级 H 与通胀值 L 的定义

- 全局通胀值 `L`：精确有理数，初始为 `0`。
- 每个条目记录：
  - `freq`：访问频次，插入时为 `1`，每次命中的 `Get` 加 `1`。
  - 优先级 `H = L + freq×cost/size`，在**插入或命中那一刻的 `L`**
    上计算并保存（之后 `L` 变化不回头改写旧条目）。
  - `last`：最近一次插入或命中时的逻辑序号 `tick`。
- `tick` 从 `0` 开始，每次**成功的 Put 插入**与每次**命中的 Get** 各加 `1`；
  未命中 `Get`、被拒绝的操作都不增加 `tick`。

## 驱逐与并列规则

插入时若 `已用字节 + size > Cap`，反复驱逐当前 **`H` 最小**的条目：

1. `H` 较小者先驱逐；
2. `H` 相等时，`last` 较小（更久未被插入/命中）者先驱逐；
3. 每驱逐一个，立即令 `L = 被驱逐者的 H`，因此连续驱逐时 `L`
   逐个、单调地推进（不是清零，也不取平均值）；
4. `已用字节 + size == Cap` 时**不驱逐**；
5. 装得下后插入新条目，**不做准入过滤**：新条目 `H` 允许低于现有条目。

驱逐由二叉最小堆（`container/heap`）支持，单次 `Put`/`Get`
的 `H` 比较次数为 O(log n)，不随条目数线性增长。

## 覆盖写语义

`Put` 到已存在的键时：

1. 先移除旧条目并释放其字节，**不改变 `L`**；
2. 再用新 `size`/`cost` 走标准插入流程（可能因新尺寸触发驱逐）；
3. 新条目 `freq` 重置为 `1`，`H` 用当前 `L` 计算。

## API

- `NewCache(capBytes int64) (*Cache, error)`：以字节容量构造。
- `Put(key string, size, cost int64) (evicted []string, err error)`：
  返回本次按驱逐先后排列的键列表；无驱逐时为 nil。
- `Get(key string) (bool, error)`：命中时 `freq++`、用当前 `L`
  重算 `H`、更新 `last`；未命中不改任何状态。
- `Peek(key string) (EntryView, bool, error)`：只读返回
  `Freq` / `H`（`*big.Rat` 副本）/ `Last`，不改状态。
- `Used() int64`：当前已用字节。

`Put`、`Get`、`Peek`、`Used` 均在互斥锁下执行，可并发调用，
其结果等价于某个串行顺序。

## 错误与拒绝优先级

所有拒绝原因都是可区分的哨兵错误（用 `errors.Is` 判定）：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidCapacity` | 构造时 `Cap <= 0` |
| `ErrEmptyKey` | 键为空字符串（`Put`/`Get`/`Peek`） |
| `ErrInvalidSize` | `size <= 0` |
| `ErrInvalidCost` | `cost < 1` |
| `ErrObjectTooLarge` | `size > Cap` |

`Put` 严格按 **键为空 → `size<=0` → `cost<1` → `size>Cap`**
的顺序只报第一个错误。任何被拒绝的操作（含因 `size>Cap`
被拒的覆盖写）都在改动状态之前返回，不改变条目、`L` 与 `tick`。

## 本地验证

```bash
# 全部测试（功能 + 2000 组对拍 + 复杂度证明 + 并发）
go test ./ontology/ -v

# 竞态检测
go test -race ./ontology/

# 仅看复杂度证明（含每次操作的 H 比较次数日志）
go test ./ontology/ -run TestComparisonComplexity -v

# 对拍朴素实现并查看输入/输出/判定依据日志
go test ./ontology/ -run TestDifferentialAgainstNaive -v

go vet ./...
gofmt -l .
```

### 测试覆盖要点

- `L` 在驱逐时推进为被驱逐者的 `H`（非 0、非平均值）；
- 命中后用当前 `L` 重算 `H`，不沿用旧 `H`；
- 一次 `Put` 连续驱逐多个时 `L` 逐个单调推进；
- `H` 相等时驱逐 `last` 较小者；
- 覆盖写先移除旧条目且不推进 `L`；
- `已用 + size == Cap` 时不驱逐；
- 新条目 `H` 低于现有条目仍被插入；
- 大数据例（`L=2^53` 处 `L+1` 会被 `float64` 舍入为 `L`）
  证明必须使用精确有理数；
- 拒绝次序与被拒操作的状态不变性；
- 与“每次线性扫描求最小 `H`”的朴素实现对拍 2000 组随机序列；
- 在 1000 与 50000 个条目（size=1、Cap=条目数）下，用非导出计数器
  证明「新键 Put 恰驱逐一个」「覆盖写 Put」「Get 命中」三类操作的
  单次 `H` 比较次数都不超过 `8×(⌊log2(n)⌋+2)`。
