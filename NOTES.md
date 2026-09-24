# NOTES：带滞后上限的一致性读

## 八步推导（初始 H=-1, A=-1；Read 的 T=H-lag 在调用时刻冻结）

| # | 操作 | H | A | Pos | Downgraded |
|---|------|---|---|-----|------------|
| 1 | Commit(0) | 0 | -1 | | |
| 2 | Commit(1) | 1 | -1 | | |
| 3 | Commit(2) | 2 | -1 | | |
| 4 | Apply(1) | 2 | 1 | | |
| 5 | Commit(3) | 3 | 1 | | |
| 6 | Read(2,Downgrade) | 3 | 1 | 1 | false（T=1，A=1 不劣于 T） |
| 7 | Commit(4) | 4 | 1 | | |
| 8 | Read(2,Downgrade) | 4 | 1 | 1 | true（T=2，A=1<T） |

- (甲) 第6步 Pos=1, Downgraded=false；第8步 Pos=1, Downgraded=true。若错写成「A>T 才算新鲜」（即降级=A<=T）：第6步 A=1=T → 错成 true；第8步 A=1<T=2，错式仍得 true，不受影响。
- (乙) 第6步换 Read(2,Block)：T=1，A=1>=T，不阻塞，Pos=1。若降级模式错成返回 T 而非 A：第8步 Pos 错成 2——2 已提交但未应用，读端实际只能看到 A=1，返回未应用位点违反不变量1（Pos 恒等于 Applied()）。
- (丙) 追加 Commit(5) 后 H=5,A=1，Read(2,Downgrade)：T=3，Pos=1，Downgraded=true。再 Apply(3) 后 A=3，Read(2,Downgrade)：T=3，Downgraded 翻回 false。轨迹 false→true→true→false，故 Downgraded 非单调。lag=0 时 Block 的 T=H，要求返回时 Applied>=H，连同 A<=H 得 Applied==Head 即强一致，由不变量3（阻塞不滞后）保证。

## 四条不变量的保证位置与钉住测试

1. 与朴素参照一致：lag.Reader.Read 的 Downgrade 分支直接返回 st.A 与 st.A<T（lag/lag.go）；测试 TestDowngradeMatchesNaive。
2. 单调：fresh.State.Commit/Apply 的越界拒绝与 +1 推进（fresh/fresh.go）；测试 TestMonotonic。
3. 阻塞不滞后：lag.Reader.Read 的 Block 分支冻结 T、等待 Apply 唤醒后返回 st.A（lag/lag.go）；测试 TestBlockNotStale、TestConcurrentBlockWake。
4. 失败不留痕：fresh 校验先于赋值、lag 在校验通过前不碰等待堆（fresh/fresh.go、lag/lag.go）；测试 TestFailureNoTrace。

复杂度：唤醒按 T 最小堆弹顶，检查数存非导出字段 checked，测试 TestWakeChecksBounded 断言不随 m 增长。
