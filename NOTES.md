# NOTES — ontology-265

记号 `(kabc)≡(K=k,A=a,B=b)`，后缀 `+`/`−` 与数字为带符号多重性。三项公式：`J(R+dR,S+dS)−J(R,S)=dR⋈S旧 + R旧⋈dS + dR⋈dS`（两个对侧表都取**批前**，由 (R+dR)(S+dS) 展开即得）。

| 批 | 项（对侧表状态） | 该项全部带符号元组 |
| 1 | T1 dR⋈S旧 | ∅ |
| 1 | T2 R旧⋈dS | ∅ |
| 1 | T3 dR⋈dS | (1xp)+ (2yq)+ ｜输出同 T3｜视图 {(1xp):1,(2yq):1} |
| 2 | T1 dR⋈S旧 | (1zp)+ (2yq)− |
| 2 | T2 R旧⋈dS | (1xr)+ (2yq)+ |
| 2 | T3 dR⋈dS | (1zr)+ (2yq)− ｜输出 (1xr)+ (1zp)+ (1zr)+ (2yq)− ｜视图 (1xp)(1xr)(1zp)(1zr) 各 1 |
| 3 | T1 dR⋈S旧 | (1xp)− (1xr)− |
| 3 | T2 R旧⋈dS | (1xr)− (1zr)− |
| 3 | T3 dR⋈dS | (1xr)+ ｜输出 (1xp)− (1xr)− (1zr)− ｜视图 {(1zp):1} |

**甲（批2）** 后后省T3 ≡ 正确输出 + T3：输出 (1xr)+ (1zp)+ (1zr)+2 (2yq)−2；多出 (1zr) 的 +1、(2yq) 的 −1（多删一重），应用后 (2yq)=−1。前前省T3 ≡ 正确输出 − T3：输出仅 (1xr)+ (1zp)+；少 (1zr)+1 与 (2yq)−1，视图残留 (2yq):1、缺 (1zr)。
**乙（批3）** 后后省T3：输出 (1xp)− (1zr)−，少 (1xr) 的 −1（该删未删），(1xr) 残留 1；前前省T3：(1xp)− (1xr)−2 (1zr)−，(1xr)=−1。方向反转：批2 在 key1 两侧同"增"，叉积多加一个新元组 (1zr)、并把旧元组 (2yq) 多删；批3 两侧同"删"，(−)(−)=+ 作用在将删的旧元组 (1xr) 上——后后写法多出的叉积抵消删除（表现为少删、残留），前前写法缺失叉积则连删两次（表现为多删、变负）。
**丙** 只有第 3 批错：T3 中 (−(1x))(−(1r)) 被写成 −1（整数乘法应为 +1），(1xr) 输出 **−3**（正确 −1，多删 2 重）；批1 全为 ++、批2 为 ++ 与 −+，误规则恰与乘法同号故不误。三批后正确 `View()` = **{(1,z,p):1}**。

不变量位置与钉住测试：
1. View≡全量重算：`djoin/engine.go` Feed 在校验与超限检查全部通过后才 commit（r/s/视图同一把锁内提交）；`TestViewMatchesRecompute`。
2. 每批差分≡全量求差：`djoin/engine.go` `terms()` 三项齐全（dR⋈S旧 / R旧⋈dS / dR⋈dS），`sorted`（selfcheck.go）合并且去零排序；`TestDeltaMatchesNaive`。
3. 前缀非负、零即删：`rel/rel.go` `Add` 归 0 删 V 删 K，`engine.go` commit 时归 0 删元组；`TestNonNegPrefixes`。
4. 失败不留痕：`engine.go` Feed 先 `aggregate` 验 Sign/V（ErrInvalidChange）、`checkNonNegative`（ErrDeleteMissing）、`checkViewLimit`（ErrViewLimit），全过才改状态；`TestRejectedBatchAtomic`。
