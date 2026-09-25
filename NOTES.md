# NOTES — arena bump allocator（arenaSize=16, align=8, maxArenas=3）

## 八步分步表（返回 (偏移,代数)；bump 为各 arena bump 位置，- 表示尚未创建）

| 步 | 操作 | 返回 | bump0 | bump1 | bump2 |
|---|---|---|---|---|---|
| 1 | Alloc(5)  sz=8 | (0, 0) | 8 | - | - |
| 2 | Alloc(10) sz=16，arena0 余 8 放不下 | (16, 0) | 8 | 16 | - |
| 3 | Alloc(5)  sz=8，arena1 满 | (32, 0) | 8 | 16 | 8 |
| 4 | Alloc(3)  sz=8 | (40, 0) | 8 | 16 | 16 |
| 5 | Reset() | gen→1 | 0 | 0 | 0 |
| 6 | Alloc(5)  sz=8 | (0, 1) | 8 | 0 | 0 |
| 7 | Alloc(5)  sz=8 | (8, 1) | 16 | 0 | 0 |
| 8 | Valid(0,0) | false（0≠当前代数1） | 16 | 0 | 0 |

- (甲) 不对齐时第二次 `Alloc(3)` 错返 **off=5**（[0,5) 占 5 字节、[5,8) 占 3 字节，紧密堆放）；正确为 **off=8**，两次各保留 **8** 字节（[0,8)、[8,16)，前 3 字节是对齐填充）。
- (乙) 允许跨边界时错返 **off=8**，对象横跨 **arena0[8,16) + arena1[16,24)**（各 8 字节）；正确为 **off=16**，对象整块在 arena1[16,32)，旧 arena 尾 **[8,16) 共 8 字节作废**。
- (丙) Reset 忘加代数：gen 仍为 0，`Valid(0,0)` 错返 **true**；正确应返 **false**。调用者会把悬垂偏移 0 当活引用——而 Reset 后第 6 步新对象恰好又占 off=0，旧引用遂**别名(alias)新一代对象**，造成跨代脏读/误写而无人察觉。

## 四条不变量的保证位置与钉住测试

1. 守恒与不重叠：`bump.Alloc`（bump/bump.go）仅在 `arenaSize-bump>=sz` 时提交、`bump+=sz`，跨块整块搬移；钉于 `TestInvariantConservation`、`TestConcurrentAlloc`。
2. 与朴素参照一致：`bump.Alloc` 的对齐/不跨块/尾作废即朴素规则；`bump.NaiveSim` 参照实现逐操作比对，钉于 `TestInvariantNaiveReference`、`TestSelfCheck`。
3. 对齐与悬垂检测：偏移由 `arena.AlignUp` 后的 bump 产生（恒为 align 倍数）；`bump.Valid` 只比 `gen==当前代数`，`Reset` 必 +1；钉于 `TestInvariantAlignmentDangling`。
4. 失败不留痕：构造参数在 `bump.New` 建状态前校验；`Alloc` 对 n 与 arena 用尽的判定全部在写 bump/cur/gen 之前；钉于 `TestRejectedOpsLeaveNoTrace`、`TestFourSentinelErrors`。
