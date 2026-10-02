# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 网络缓冲去膨胀控制器（`ontology` 包）

`ontology.Deflator` 是一个带滑动吞吐窗口、迟滞确认、振荡抑制与内存池上限的
每通道网络缓冲区大小控制器。它把各采样周期测得的吞吐量换算为
「目标时延内的在途数据量」，并在大小决策上施加迟滞，使输出可精确复现。

### 目标时延与每通道期望大小

窗口保留最近至多 `W` 个采样 `(bytes, dt)`（`dt` 单位毫秒）。设窗口内
`bytes`、`dt` 之和为 `Sb`、`Sd`：

- 吞吐率 `R = floor(Sb*1000/Sd)`（字节/秒，向下取整）；
- 目标时延 `T` 毫秒对应的在途数据量 `Dt = ceil(R*T/1000)`；
  `R*T` 用 128 位中间值计算（实现基于 `math/bits` 的 128 位除法）；
- `C` 个通道均分，每通道期望 `per = ceil(Dt/C)`；
- `raw = min(max(per, Bmin), Beff(C))`，候选大小 `cand = floor(raw/G)*G`
  （按粒度向下取整；所有乘积不超过 int64）。

### 内存池有效上限与强制收缩

`Beff(C) = min(Bmax, floor(Pool/(C*G))*G)`。构造要求
`Beff(C0) >= B0`，否则配置整体拒绝（`ErrInvalidConfig`）。
`SetChannels(C')` 在 `Beff(C') < Bmin` 时以 `ErrCapacity` 拒绝；
若 `cur > Beff(C')` 则**强制收缩** `cur = Beff(C')`（不受缩小阈值约束、
不经确认），置污染标记 `pol`，计一次强制收缩；`C' == C` 为空操作。

### 阈值与确认的迟滞规则

- 增大：`(cand-cur)*100 >= cur*thU` 或 `cand == Beff(C)`（撞顶绕过阈值）
  时，确认计数 `streak++`；`streak >= need` 才 `Applied`。
  通常 `need = Kc`；振荡抑制余量 `damp > 0` 时 `need = 2*Kc`。
- 缩小：`(cur-cand)*100 >= cur*thD` 或 `cand == Bmin`（触底绕过阈值）
  时立即 `Applied`，不需要确认。
- `cand == cur` 为 `Hold`；阈值不满足也是 `Hold`；任何 `Hold` 或生效的
  缩小都把 `streak` 清零，增大确认必须连续累计。

### 振荡抑制：方向反转、gap 与 damp

每次 `Applied` 与强制收缩称为一次**改变**，方向为增或缩（强制收缩恒为缩）：

- `gap`：自上次改变以来的有效样本数。样本开始处理时若 `lastDir != 无`
  则先 `gap++`（Skipped 样本与被拒绝样本不推进 `gap`；改变发生的那个样本
  也会计入本次 `gap`，随后归零）。
- 若本次方向与 `lastDir` 相反且 `gap <= H`，则 `damp = H`（`H=0` 时不设置）。
  随后 `lastDir = 本次方向`、`gap = 0`。
- `damp` 在之后每个未被 Skipped 的样本处理结束时减一；**设置 `damp` 的
  那个样本不递减**；Skipped、被拒绝、`SetChannels` 与 `Resume` 都不改变
  `damp`（除新的方向反转改变重新设置它）。
- 样本的 `need` 按该样本**开始时**的 `damp` 取值，因此设置当样本按旧
  `damp`（及其不衰减规则）处理。

### 污染样本跳过（pol）

每次 `Applied` 或强制收缩后置 `pol = true`。之后下一个未被拒绝的
`Sample` 返回 `Skipped` 并仅清除 `pol`：不入窗口、不动 `streak`、`gap`、
`damp`，也不产生大小决策。`pol` 只由 `Sample` 清除，`Pause`/`Resume`
都不清除。这保证每次改变后总有一个采样周期不参与决策。

### 暂停与恢复

- `Pause()` 置暂停标记；暂停中的 `Sample` 以 `ErrPaused` 拒绝。
  拒绝原因顺序为「参数非法 → 暂停」，只报第一个；被拒绝操作不改变任何状态。
- `Resume()` 清除暂停标记、清空窗口、`streak=0`；`pol`、`gap`、`damp`、
  `lastDir` 全部保留。
- `SetChannels` 的拒绝顺序为「参数非法 → 容量不足」。

### 状态与计数

`State()` 返回 `cur`、`C`、窗口（按时间顺序复制）、`streak`、`pol`、
暂停标记、`lastDir`、`gap`、`damp`，以及累计的 `Applied`、强制收缩、
`Skipped` 次数。未导出计数器 `windowOps` 统计窗口的入与出：未满时每样本
1 次（仅入），已满后每样本恒为 2 次（1 出 1 入），与 `W` 无关。

所有方法均在互斥锁下执行，可并发调用，结果等价于某个串行顺序；任意时刻
`Bmin <= cur <= Beff(C)` 且 `G | cur`，且只有 `Applied` 与强制收缩会
改变 `cur`。

### 本地验证

```bash
# 若 go 不在 PATH：
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache

go test -race -count=1 ./...
go test -run TestSpecExample -v ./ontology   # 题面示例逐步核对
go test -run TestRandomNaive -v ./ontology   # 2000 组随机序列朴素模拟对照
go vet ./...
gofmt -l .
```

`fuzz_test.go` 中的 `naiveSim` 是按上述规则逐步重写的朴素参考实现
（每次重新求和窗口，不走环形缓冲与增量求和），2000 组随机操作序列下
控制器的每次返回值、完整状态与计数器都与其逐项比对；失败时日志打印
此前每一步的输入、输出与判定依据（R、cand、cur、streak、gap、damp 等）。

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
