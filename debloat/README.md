# debloat — 网络缓冲去膨胀控制器

`debloat.Controller` 依据各采样周期观测到的吞吐量，把每通道缓冲区大小调到
「目标时延内的在途数据量」附近，并通过滑动窗口、迟滞确认、污染跳过、振荡抑制
与内存池上限保证大小决策可以逐位精确复现。所有方法均以互斥锁保护，并发调用
等价于某个串行执行顺序。

## 目标时延与每通道期望大小

每个被接受的样本 `Sample(bytes, dt)` 先进入至多容纳 `W` 个样本的滑动窗口；
窗口满时挤出最旧样本。对窗口内的总量求和：

- 吞吐量：`R = floor(Sb × 1000 / Sd)`，其中 `Sb`、`Sd` 是窗口内 bytes 与
  dt（毫秒）之和；
- 在途数据量：`Dt = ceil(R × T / 1000)`，即在目标时延 `T` 毫秒内的在途字节数；
- 每通道期望：`per = ceil(Dt / C)`，`C` 为当前通道数。

因为 `R × T` 最大可达约 `10^21`，计算全程使用 128 位等价的大整数
（`math/big`），仅在夹紧到 `int64/uint64` 范围后再转为普通整数。

期望大小随后夹紧并按粒度取整：

```
raw  = min(max(per, Bmin), Beff(C))
cand = floor(raw / G) × G
```

内存池有效上限为

```
Beff(C) = min(Bmax, floor(Pool / (C × G)) × G)
```

构造时要求 `1 ≤ G ≤ Bmin ≤ B0 ≤ Bmax ≤ 2^30`，`G` 同时整除 `Bmin/B0/Bmax`，
并且 `Beff(C0) ≥ B0`；任何越界（含 `T ∈ [1,10^6]`、`W ∈ [1,100]`、
`thU ∈ [0,1000]`、`thD ∈ [0,100]`、`Kc ∈ [1,100]`、`C0 ∈ [1,10^4]`、
`Pool ∈ [1,2^40]`、`H ∈ [0,1000]`）都作为「配置非法」整体拒绝，返回
`ErrIllegalConfig`。

## 阈值与确认（迟滞规则）

- `cand == cur`：`Hold`，`streak = 0`。
- 增大：当 `(cand-cur)×100 ≥ cur×thU`（等号成立即触发）或 `cand == Beff(C)`
  时，`streak++`；`streak ≥ need` 才 `Applied`，否则 `Pending`。
  - `need = Kc`；振荡抑制余量 `damp > 0` 时 `need = 2×Kc`。
  - `Applied` 后 `cur = cand`、`streak = 0`、置污染标记 `pol`。
  - 阈值差 1（`×100` 后小 1）不触发，结果为 `Hold` 且 `streak = 0`。
- 缩小：当 `(cur-cand)×100 ≥ cur×thD`（等号成立即触发）或 `cand == Bmin`
  时立即 `Applied`，无需确认；否则 `Hold` 且 `streak = 0`。
- 任何 `Hold` 与缩小都会把 `streak` 清零，确认过程可被两者打断后重新计数。

## 振荡抑制：方向反转、gap 与 damp

每次 `Applied` 与强制收缩都记为一次「改变」，方向为 `up` 或 `down`：

- 若此前已有改变（`lastDir != none`）、本次方向与上次相反，且改变发生时的
  `gap ≤ H`，则把 `damp = H`（`H = 0` 时恒为 0）；
- 随后 `lastDir = 本次方向`、`gap = 0`。

`gap` 是距上次改变的「有效样本数」：只有被接受且未 `Skipped` 的 `Sample`
才会在处理开始时令 `gap++`；`Skipped`、暂停期间与参数非法的样本都不推进
`gap`。因此 `gap == H` 触发抑制、`gap == H+1` 不触发。

每个未被 `Skipped` 的样本在处理结束时令 `damp--`（当 `damp > 0`），但
**刚刚设置 `damp` 的那个样本不递减**；本样本的 `need` 取处理开始时的
`damp` 计算。`SetChannels` 与 `Resume` 都不改动 `damp`，`damp` 始终落在
`[0, H]`。

## 污染样本（pol）

每次 `Applied` 或强制收缩后 `pol = true`。之后下一个未被拒绝的 `Sample`
恰好是 `Skipped`：清除 `pol` 并立即返回，样本不进入窗口，`streak`、`gap`、
`damp` 全部不变，并累计 `SkippedCount`。`pol` 只能被 `Sample` 清除；
`Pause()` 与 `Resume()` 都不清除它。

`Sample` 的拒绝顺序为：参数非法（`bytes ∈ [0,2^30]`、`dt ∈ [1,10^6]`，
否则 `ErrIllegalArgument`）优先，其次暂停（`ErrPaused`）。

## 内存池与强制收缩（SetChannels）

`SetChannels(C′)`：

1. `C′ ∉ [1,10^4]` 返回 `ErrIllegalArgument`（参数非法优先）；
2. `C′ == C` 是空操作，不改动任何状态（包括 `streak`）；
3. `Beff(C′) < Bmin` 返回 `ErrInsufficientCapacity`（容量不足第二优先）；
4. 否则 `C = C′`、`streak = 0`；若 `cur > Beff(C′)`，无条件强制收缩
   `cur = Beff(C′)`、`pol = true`、累计 `ForcedCount`，方向恒为 `down`，
   并按上面的方向/gap/damp 规则记账（因此与近期的 `up` 反转时也会触发
   振荡抑制；与上一次同为 `down` 则不重新置 `damp`）。

`Pause()` 只置暂停标记；`Resume()` 清除暂停标记并清空窗口、`streak = 0`，
但保留 `pol`、`gap`、`damp`、`lastDir`。

## 状态与计数器

`State()` 返回当前 `Cur`、`Channels`、窗口长度、`Streak`、`Polluted`、
`Paused`、`LastDir`、`Gap`、`Damp`，以及累计的 `AppliedCount`、
`ForcedCount`、`SkippedCount`。

非导出计数器 `windowOps`（`Snapshot.WindowOps` 可见）统计窗口的入与出：

- 窗口未满：本次样本 1 次入操作，增量 1；
- 窗口已满：1 次出（挤出最旧）+ 1 次入，增量 2。

因此每个被接受且未 `Skipped` 的样本至多 2 次操作，且窗口一旦填满，增量
与 `W` 无关（`W=3` 与 `W=100` 两档在同类样本上的增量相同）。
`Skipped` 与被拒绝的样本增量为 0；`Resume` 清空窗口也不计数。

恒成立的不变量：任何时刻 `Bmin ≤ cur ≤ Beff(C)`、`G | cur`；只有
`Applied` 与强制收缩会改变 `cur`；相同操作序列重放得到完全相同的返回值、
状态与计数。

## 本地验证

```bash
# 常规测试
go test ./debloat

# 竞态检测 + 全量
go test -race -count=1 ./...

# 详细日志（2000 组随机序列的输入、输出、判定依据均通过 t.Logf 打印）
go test ./debloat -run TestRandomizedAgainstNaive -v

go vet ./...
gofmt -l .
```

`naive_test.go` 是按规则逐条写成的朴素模拟器：窗口保存为普通切片，每次
重新求和并用大整数重算 `R/Dt/per`。`fuzz_test.go` 用确定性种子生成 2000
组随机合法配置与随机操作序列（含非法参数、暂停/恢复、通道伸缩），逐步比对
返回值与全部状态/计数器；`controller_test.go` 覆盖窗口填满前后的取整、
阈值恰等与差 1、确认打断、污染跳过、强制收缩、gap=H/H+1、damp 不递减与
need 加倍、计数器两档对照及并发不变量。
