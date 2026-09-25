# FINDINGS: 版本号推进边界与文档承诺的不一致

调查对象：`manager.go` 的 `Update`、`lifecycle.go` 的 `SwitchTo`。
行为均已由 `noop_update_test.go` 中的 characterization tests 钉住（断言当前真实行为，全部通过）。

## Finding 1：空 changes 与非空但全 no-op 的 Update 行为不对称

- 复现输入：初始版本 1（host=a, port=80, retries=3）。
  - a) `Update({})` 或 `Update(nil)`
  - b) `Update({"host":"a","port":80,"retries":3})`（所有值与当前相同）
- 实际输出：
  - a) 返回 1，`CurrentVersion()` 仍为 1，不产生新版本。
  - b) 返回 2，`CurrentVersion()` 变为 2，克隆出一个内容与全部字段 source 逐字段相同的新版本（host/port/retries 的 source 仍全为 1）。
- 应当输出：两种形态行为一致——全 no-op 的 Update 应像空 changes 一样原地返回当前版本号，不推进版本、不克隆空壳版本。
- 根因分析：`manager.go` 中 `Update` 对 `len(changes) == 0` 提前 `return m.current`，但只要 changes 非空，`nv := m.next; m.next++` 在校验通过后无条件执行；随后的 `if vs.values[name] == v { continue }` 只抑制 source 推进，没有任何「整批均未改变任何值」的检测，于是版本号推进与 source 推进脱钩，产出「版本号 +1、provenance 原封不动」的空壳版本。注释只承诺「source version advances only when its value actually changes」，对版本号本身何时推进未作说明，「Update applies changes and returns the new version number」一句甚至与空 changes 返回旧版本号的现实相悖。

## Finding 2：连续全 no-op Update 产生空壳版本链

- 复现输入：连续 3 次 `Update({"host":"a","port":80,"retries":3})`。
- 实际输出：返回版本号 2、3、4 连续 +1；版本 1–4 内容逐字段相同；所有字段 source 始终为 1。每个空壳版本都留在 `m.versions` 中，只能靠 GC 回收。
- 应当输出：版本号不推进（同 Finding 1）；至少不应为零信息变更分配永久单调递增的版本号。
- 根因分析：同 Finding 1，是同一缺陷的累积表现——版本号推进无任何「是否有实际变更」的前置条件，no-op 写入次数直接膨胀版本链长度与内存占用。

## Finding 3：SwitchTo 当前版本自身不是 no-op

- 复现输入：`Update({"host":"b"})` 后 current=2，调用 `SwitchTo(2)`。
- 实际输出：返回 3，`CurrentVersion()` 变为 3；版本 3 的内容与 sources 与版本 2 逐字段相同。
- 应当输出：切换到当前版本自身应为 no-op，直接返回当前版本号 2，不产生新版本。
- 根因分析：`lifecycle.go` 的 `SwitchTo` 只校验目标版本存在，随后无条件 `nv := m.next; m.next++` 并 `src.clone()`，没有 `version == m.current` 的特判。函数注释强调「always produces a brand-new, larger version number」，把「切到自身也复制」固化为承诺，但与「版本号只在内容变化时推进」的直觉语义（以及 `Update` 空 changes 的 no-op 先例）不一致。

## Finding 4：SwitchTo 后字段 provenance 归属与「谁使它成为当前」不区分

- 复现输入：v2 写 host=b，v3 写 port=8080，然后 `SwitchTo(1)` 得 v4。
- 实际输出：v4 中 host/port/retries 的 `SourceVersion` 全为 1（最初写入它们的版本），而非本次切换产生的 4。
- 应当输出：语义需要明确并文档化——要么切换把 source 重写为切换版本号（「谁把它变成当前」），要么保留原始写入者但注释须说明 switch 路径不触碰 provenance。当前两条语义（「source 指谁写的」与「谁把它变成当前」）在切换路径上混用且无任何注释覆盖。
- 根因分析：`SwitchTo` 直接 `src.clone()`，sources 映射原样复制，没有任何 provenance 重写；`Update` 的注释「source version advances only when its value actually changes」只描述了 Update 路径，对 SwitchTo 路径下 source 的归属保持沉默。

## 汇总

| # | 位置 | 问题 | 测试 |
|---|------|------|------|
| 1 | `manager.go` `Update` | 非空全 no-op 仍推进版本号，与空 changes 不对称 | `TestUpdateNoOpForms` |
| 2 | `manager.go` `Update` | 连续 no-op 产生空壳版本链，source 不变 | `TestRepeatedNoOpUpdates` |
| 3 | `lifecycle.go` `SwitchTo` | 切到自身不是 no-op | `TestSwitchToCurrentVersion` |
| 4 | `lifecycle.go` `SwitchTo` | 切换后 source 指向原始写入版本，归属语义未文档化 | `TestSwitchToSourceProvenance` |

本次仅补充测试与文档，未改动任何实现文件的行为。
