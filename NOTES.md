# NOTES — serial number arithmetic（N=4, M=16, half=8）

| step | s  | d | rel          | abs | last |
|---|---|---|---|---|---|
| 1 | 13 | — | first        | 13  | 13 |
| 2 | 14 | 1 | Less         | 14  | 14 |
| 3 | 15 | 1 | Less         | 15  | 15 |
| 4 | 0  | 1 | Less(wrap)   | 16  | 16 |
| 5 | 1  | 1 | Less         | 17  | 17 |
| 6 | 2  | 1 | Less         | 18  | 18 |
| 7 | 10 | 8 | Incomparable | err | 18 |
| 8 | 3  | 1 | Less         | 19  | 19 |

(甲) d=(0-15)&15=1：&15 取的是模 16 的前向距离，15 是后向距离，故不是 15。朴素无符号直接比较会因 0<15 把 0 判成倒退/旧（Greater）而拒绝，回绕展开 16 丢失，破坏不变量 1（与参照一致）和 3（绝对单调）。
(乙) Cmp(2,10)：d=(10-2)&15=8，判 Incomparable。若把 d==8 误归 Less，10 会被错展成 18+8=26（应拒绝、last 停 18）。+8 ≡ -8 (mod 16)，前移 8 与后移 8 同余，半圈处二者无法区分。
(丙) Cmp(2,10) 的 d=8，Cmp(10,2) 的 d=(2-10)&15=8；互反方向同值，若都归 Less 则双向皆 Less，违反不变量 2（反对称），故 d==M/2 必须单列为 Incomparable。

## 不变量（代码位置 / 钉住测试）

1. 与朴素参照一致：`unwrap.go` Feed 逐分支按 d 手推展开 —— `TestReferenceModel`、`TestFeedTable`
2. 反对称：`sar.go` Cmp 将 d==M/2 单列为 Incomparable —— `TestCmpAntisymmetry`
3. 绝对单调：`unwrap.go` Less 分支 last+=d、Equal 不前进 —— `TestFeedMonotonic`
4. 失败不留痕：`unwrap.go` 所有校验在写 last 之前 return —— `TestRejectLeavesLast`

复杂度：每次 Feed 只与 last 比较一次（检查计数=1，首值记 1），白盒测试 `TestFeedChecksO1` 钉在 unwrap 包内，计数器不导出。
