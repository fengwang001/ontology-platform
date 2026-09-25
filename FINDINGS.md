# FINDINGS：版本号推进边界与文档承诺不一致

范围：仅记录观察到的当前实现行为（由 `noop_characterization_test.go`
钉住，全部通过），不修改任何实现。

## 1. 非空但全 no-op 的 `Update` 仍推进版本号，与空 `Update` 不对称

- 涉及实现：`manager.go` `Update`
- 复现输入：
  - 初始版本 1（`host=a, port=80, retries=3`）。
  - `Update(nil)` 或 `Update({})`。
  - `Update({"host":"a"})`（非空，但值与当前值相同）。
  - 连续重复 3 次。
- 实际输出：
  - 空 changes（含 nil map）：返回 1，`CurrentVersion()` 保持 1，不产生新版本。
  - 非空全 no-op：返回 2→3→4，每次 `CurrentVersion()` 都推进；新版本与旧版本的
    `values` 和 `sources` 逐字段完全相同（空壳版本）。连续 N 次版本号恰好 +N，
    所有字段 source 始终不变。
- 应当输出：两种「没有任何字段实际变更」的输入语义应对称——要么都原地不动
  （返回当前版本号、不分配新版本），要么文档明确说明非空 changes 即使无变化也
  强制产生新版本。按注释「source version advances only when its value actually
  changes」与整体「无变更则不推进」的口径，期望非空全 no-op 也返回当前版本、
  版本号不推进。
- 根因：版本号推进与字段 source 推进是两条独立路径。`len(changes)==0` 时提前
  `return m.current`；而只要 changes 非空，校验通过后立即无条件
  `nv := m.next; m.next++` 并 `clone()`，随后的循环才用 `vs.values[name] == v`
  逐字段决定是否写值/推进 source。实现没有统计「是否真有字段发生变化」，因此
  「全相等」与「至少一个变化」无法区分，版本号一律推进。

## 2. `SwitchTo(current)` 切到当前版本自身也产生新版本

- 涉及实现：`lifecycle.go` `SwitchTo`
- 复现输入：
  - 初始版本 1；或先 `Update({"host":"b"})` 使当前为 2。
  - 对当前版本自身连续调用 3 次 `SwitchTo(m.CurrentVersion())`。
- 实际输出：每次都分配下一个版本号（2→3→4，或 3→4→5），`CurrentVersion()`
  随之变化；新版本内容与 provenance 与上一版逐字段相同。
- 应当输出：`SwitchTo(x)` 在 `x` 已是当前版本时应为 no-op，返回当前版本号、
  不克隆、不推进（与空 `Update` 的原地语义一致）。至少文档需要明确声明
  「切到自身也强制复制新版本」这一反直觉行为。
- 根因：`SwitchTo` 只校验目标版本存在（`m.versions[version]`），没有
  `version == m.current` 的短路分支；随后无条件 `nv := m.next; m.next++`、
  `m.versions[nv] = src.clone()`、`m.current = nv`。注释「It always produces a
  brand-new, larger version number」字面上描述了该行为，但与「无变更则无新
  版本」的整体语义（空 Update、校验失败均不推进）不一致，且未点明自切换这个
  边界。

## 3. `SwitchTo` 后字段 provenance 指向「最初写入版本」，而非「激活它的切换版本」

- 涉及实现：`lifecycle.go` `SwitchTo`（`src.clone()` 连同 `sources` 原样复制）
- 复现输入：
  - v1：`host=a, port=80, retries=3`（source 均为 1）。
  - v2：`Update({"host":"b"})`。
  - v3：`Update({"port":8080})`。
  - v4：`SwitchTo(1)` 把初始内容恢复为当前。
  - 再 v5：`SwitchTo(v4)`、以及 v6：`Update({"retries":7})`。
- 实际输出：v4 的内容等于 v1，且 `sources = {host:1, port:1, retries:1}`——
  指向最初写入的 v1，而非切换产生的 v4；v5 再次克隆后 source 仍为 1；v6 中
  只有真正被写的 `retries` source 变为 6，其余字段仍指向 1。
- 应当输出：二选一并写入文档：
  1. provenance 表示「谁把该值变成当前」：切换时被重新激活的字段 source 应记为
     切换产生的新版本号；
  2. provenance 表示「该值最初由谁写入」（current 行为）：则应在 `Snapshot`
     文档中明确 `SourceVersion` 跨切换追溯到原始写入者。
- 根因：`versionState.clone()` 对 `values` 与 `sources` 做同等的浅拷贝；
  `SwitchTo` 复用 `clone()` 而没有任何重写 `sources` 的步骤，于是「谁写的」与
  「谁把它变成当前」两条语义在切换路径上被合并、且不可区分。`Snapshot`
  的 `SourceVersion` 注释（「the update that last wrote the given field」）
  支持当前行为，但 `SwitchTo` 注释未说明 provenance 在切换后如何归属。

## 备注

- 上述第 1、2 条都会制造「版本号推进但内容/source 完全不变」的版本，这些空壳
  版本同样占用 `versions` 存储、会被快照钉住并影响 `GC` 的回收集合。
- 修复建议（本次未实施）：在 `Update` 中以「至少一个字段归一化后的值不等于
  当前值」作为版本号推进的前置条件；在 `SwitchTo` 中对 `version == m.current`
  短路；provenance 归属按上面第 3 条选定语义后补充切换路径的 source 处理与
  文档。
