# 有界蓄存（bounded recent-key reservoir）推导与不变量

## 第三节：七步推导（N=3，slot 单调递增、驱逐后不复用）

| 步 | 事件 | 事件后集合（键(lastSeen,slot)） | 本步驱逐 | Count |
|---|---|---|---|---|
| 1 | Track(A,10) | A(10,0) | 无 | 1 |
| 2 | Track(B,10) | A(10,0) B(10,1) | 无 | 2 |
| 3 | Track(C,20) | A(10,0) B(10,1) C(20,2) | 无 | 3 |
| 4 | Track(A,5) | A(5,0) B(10,1) C(20,2) | 无（刷新，lastSeen 回退） | 3 |
| 5 | Track(D,10) | B(10,1) C(20,2) D(10,3) | A(5,0) | 3 |
| 6 | Track(E,10) | C(20,2) D(10,3) E(10,4) | B(10,1) | 3 |
| 7 | Track(F,20) | C(20,2) E(10,4) F(20,5) | D(10,3) | 3 |

- **(甲)** 第 6 步 B(10,1) 与 D(10,3) 的 lastSeen 并列 10，正确驱逐**较小 slot** 的 B(10,1)。
  若错按较大 slot 驱逐 D，第 6 步后错成：B(10,1) C(20,2) E(10,4)；正确应为：C(20,2) D(10,3) E(10,4)。
- **(乙)** 第 5 步若错写成驱逐 (lastSeen,slot) **最大**者，受害者变成 C(20,2)，留下：A(5,0) B(10,1) D(10,3)；
  正确应留下：B(10,1) C(20,2) D(10,3)。
- **(丙)** 七步后 `Count()=3`。若误把「历史上见过的不同键总数」当 `Count()` 返回，会错返回 **6**（A–F 共 6 个不同键）。
  这违反规则原话「`Count()` 返回当前集合大小，**恒等于**集合内键数，永不超过 N」——6≠3 且 6>N=3。

## 第二节四条不变量：保证位置与钉住测试

1. **容量**：`pool.Track` 仅在 `len(states)==n` 时先驱逐首元素再插入，`Count()` 直接返回 `len(states)`（pool/pool.go）；测试 `TestCapacityInvariant`（api/api_test.go）。
2. **与朴素参照一致**：`pool` 用 `rk.Less` 的同一 (lastSeen,slot) 序维护升序切片，受害者即首元素（pool/pool.go、rk/rk.go）；测试 `TestMatchesNaiveReference`（pool/pool_test.go，多档 N 与事件数逐键比对）。
3. **计数不虚增**：`Count()` 就是 `len(states)`，不存在任何独立累计值，刷新路径不改 map 大小（pool/pool.go）；测试 `TestCountNotInflated`（api/api_test.go）。
4. **失败不留痕**：`api.New`/`api.Track` 先校验（空键/负 ts/非法容量）再触碰 pool，校验失败直接返回哨兵错误（api/api.go）；测试 `TestRejectedTrackNoTrace`（api/api_test.go）。
