# 凸多边形直径（旋转卡壳）推导与不变量

## 第三节推导

五边形 v0=(0,0) v1=(5,1) v2=(6,4) v3=(3,6) v4=(1,5)，逆时针。对每条边取 |Area2| 最大的顶点为对跖顶点。

| 边 | 对跖顶点（面积） | d²(v[i],对跖) | d²(v[i+1],对跖) |
|---|---|---|---|
| e0: v0→v1 | v3（27） | 45 | 29 |
| e1: v1→v2 | v4（16） | 32 | 26 |
| e2: v2→v3 | v0（24） | 52 | 45 |
| e3: v3→v4 | v1（12） | 29 | 32 |
| e4: v4→v0 | v2（26） | 26 | 52 |

- (甲) 直径 d²=52，由 (v0,v2) 取得（e2 的 (v2,v0) 与 e4 的 (v0,v2) 是同一对）。矩形 (0,0)(6,0)(6,4)(0,4)：d²=52，并列两对 (0,2)、(1,3)；若用严格 `>` 只保留第一个达到最大值的对，会漏报 (1,3)。
- (乙) 误把直径当最长边：五边形最长边 d²=26（v0v1 与 v4v0），正确应为 52。
- (丙) 推进条件 `>` 写成 `<`：j 朝面积减小方向滑到极小处，对跖枚举全错。本五边形逐边 j 落为 v0,v1,v2,v3,v4，候选对 d² 最大仅 26（(v0,v1)、(v0,v4)），漏掉真正的直径对 (v0,v2)=52。

## 四条不变量落点

1. 与暴力一致：`api.checkInvariants` 对比 `bruteMaxD2`；测试 `TestBruteForceConsistency`。
2. 直径在对跖对：`checkInvariants` 用 `allAntipodalPairs` 逐一核验；测试 `TestReturnedPairsAreAntipodal`。
3. 并列完整：`rc.Diameter` 用 `==best` 收集全部并列并去重；测试 `TestTiesComplete`。
4. 失败不留痕：`api.New` 先 `validate` 成功才替换当前多边形；测试 `TestRejectionLeavesNoPartialResult`、`TestFaultInjectionDistinctErrors`。
