# NOTES

## 三、五步推导（初始 version=0，State={}）

| delta | 判定 | Version() | State() |
|---|---|---|---|
| D1 0->1 [Set a=1] | From==version，应用 | 1 | {a:1} |
| D2 1->2 [Set b=2] | From==version，应用 | 2 | {a:1,b:2} |
| D3 2->3 [Set c=3, Del b] | From==version，按序应用 | 3 | {a:1,c:3} |
| D4 3->4 [Set d=4] | From==version，应用 | 4 | {a:1,c:3,d:4} |
| D5 0->1 [Set a=100] | From=0 < 4，重复，跳过 | 4 | {a:1,c:3,d:4} |

- (甲) D5 被幂等跳过，a 最终 = 1；不检查 From、见 delta 就应用 → a 错成 100（正确 1）。
- (乙) D3 的 Del b 生效，b 最终不存在；只应用 Set、丢弃 Del → b 错留为 2（正确：不存在）。
- (丙) D1 的 From=0 == version=0，必须应用；误写成 From<=version → D1 被当重复跳过，a 不存在（正确 a=1）。

## 二、四条不变量的落点

1. 与顺序重放一致：`replica/replica.go` 的 Apply 在持锁区间内按序调用 `delta.Apply`；由 `TestReplayEquivalence`（随机含 gap/重复序列对拍朴素重放）钉住。
2. 版本单调：Apply 仅在全部 Change 改完后才执行 `version = d.To`，gap 直接拒绝不推进；由 `TestVersionMonotonic` 钉住。
3. 幂等去重：`From < version` 分支直接返回、不动 map 与 version；由 `TestDuplicateIdempotent`（含 D5 场景）钉住。
4. 失败不留痕：负版本/区间/空 key 全部在写 map 之前校验，整体失败；由 `TestFailureNoTrace` 钉住。
复杂度 O(1)：去重/乱序判定只读单个 version 记录（probeCount 恒为 1），由 `TestProbeCountO1`（m=100/1000/10000）钉住。
