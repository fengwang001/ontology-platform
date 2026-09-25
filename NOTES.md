# 双写对账 NOTES

## 第三节：七个写的逐步推导（记法：`值@版本`，`✝@v`=墓碑，`—`=从未写过视作版本 0）

| # | 写操作 | A 侧该键 | B 侧该键 | 分歧？ |
|---|---|---|---|---|
| 1 | Put(a,a1,1) | a1@1 | a1@1 | 否 |
| 2 | Put(b,b1,2) | b1@2 | b1@2 | 否 |
| 3 | Put(c,c1,3) | c1@3 | c1@3 | 否 |
| 4 | PutOne(A,a,a2,4) | a2@4 | a1@1 | 是 |
| 5 | DelOne(B,c,5) | c1@3 | ✝@5 | 是 |
| 6 | DelOne(A,b,6) | ✝@6 | b1@2 | 是 |
| 7 | PutOne(A,d,d1,7) | d1@7 | —(0) | 是 |

七写后快照：A={a:a2@4, b:✝@6, c:c1@3, d:d1@7}；B={a:a1@1, b:b1@2, c:✝@5, d:—}。
Reconcile：a→a2@4（A 胜）；b→✝@6（A 墓碑胜）；c→✝@5（B 墓碑胜）；d→d1@7（A 胜，B 从未写过=0）。View={a:a2, d:d1}，b、c 消失。

- (甲) 忽略版本恒以 A 为准：c 错成活值 `c1@3`（View 出现 c=c1）；正确：B 侧墓碑 ✝@5 版本更大，c 应为墓碑，View 中不存在。
- (乙) 墓碑胜者当「跳过」：b 在 B 侧残留活值 `b1@2`（A✝/B 活，永远修不平）；正确：删除传播，两侧皆 ✝@6，b 从 View 消失。
- (丙) 把「从未写过」误判为「该侧已删除」：d 会被当成 B 侧已删，若删除优先则 d 被错删（View 丢失 d=d1）。区别：从未写过=版本 0，输给任何真实版本，不产生任何传播；墓碑=带真实版本的删除记录，按版本参与胜负并可把删除传播到另一侧。

## 四条不变量：保证位置 + 钉住它的测试

1. 与批量重算一致：`dwr.Reconcile` 逐键取 Ver 大者写回两侧、`api.View` 只留活值 → `TestBatchEquivalence`（api/api_test.go）。
2. 最终一致：`dwr.Reconcile` 把胜者记录同一份写回 s.a 与 s.b → `TestConverged`（dwr/dwr_test.go）。
3. 幂等：脏集合在 Reconcile 内逐键清除，无脏键时不触碰任何状态 → `TestIdempotent`（dwr/dwr_test.go）。
4. 失败不留痕：`dwr` 写路径先完整校验（ErrKey/ErrVer/ErrVal）再改任何字段，maxVer 最后才更新 → `TestRejectNoOp`（dwr/dwr_test.go）。
