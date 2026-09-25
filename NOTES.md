# NOTES

## 八步推导（†=墓碑，冲突列加 *）

| # | 操作 | c1 | c2 | c3 | 冲突 | View(R) |
|---|------|----|----|----|------|---------|
| 1 | Put(R,c1,5,"a") | a@5 | – | – | 否 | {c1:a} |
| 2 | Put(R,c2,7,"x") | a@5 | x@7 | – | 否 | {c1:a,c2:x} |
| 3 | Put(R,c1,5,"b") | b@5 | x@7 | – | **是** | {c1:b,c2:x} |
| 4 | Put(R,c1,9,"c") | c@9 | x@7 | – | 否 | {c1:c,c2:x} |
| 5 | Del(R,c2,8) | c@9 | †@8 | – | 否 | {c1:c} |
| 6 | Put(R,c1,4,"old") | c@9 | †@8 | – | 否(4<9忽略) | {c1:c} |
| 7 | Put(R,c3,6,"y") | c@9 | †@8 | y@6 | 否 | {c1:c,c3:y} |
| 8 | Del(R,c3,2) | c@9 | †@8 | y@6 | 否(2<6忽略) | {c1:c,c3:y} |

- (甲) 平局 TS=5 两值取字典序大者 → c1=**"b"**；若平局保留旧值 → 错成 **"a"**。
- (乙) Del TS=2 < c3 现存 6，被忽略 → c3=**"y"@6**；若 Del 无条件删 → c3 错成**缺席**。
- (丙) 列级删除只影响 c2 → View(R) 里 **c1="c" 仍在**；若记录级 LWW（Key 最大 TS=9），Del@8 被整体忽略 → c2 错成 **"x" 仍可见**。

## 四条不变量的保证位置与钉住测试

1. 批量重算一致：`cell.Cell.Apply` 纯按 (TS,平局规则) 合并，与到达顺序无关 → `TestBatchEquivalence`。
2. 列隔离：`row.Row.cols` 每列独立一个 `cell.Cell`，`row.Row.Apply` 只索引并改动该列 → `TestColumnIsolation`（row 包）。
3. 墓碑语义：`cell.Cell.Apply` 平局墓碑胜、TS 更小者一律忽略；`row.Row.View` 跳过墓碑列 → `TestTombstoneSemantics`（row 包）。
4. 失败不留痕：`api.Store.Put/Del` 先做全部参数校验再碰状态（`check`+`ErrEmptyVal`）→ `TestRejectedOpsNoTrace`（api 包）。

另：定位计数器为 `row.Row.lastChecked`（非导出）→ `TestRowLookupConstant`；并发 → `TestConcurrentWrites`。
