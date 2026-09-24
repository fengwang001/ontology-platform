# RGA CRDT：八步推导与不变量索引

## 1. 八步可见文本（子元素按 (lamport, replica) 降序；跳过墓碑但仍递归其子孙）

| 步 | 操作后结构要点 | 可见文本 |
|---|---|---|
| 1 | a | `a` |
| 2 | a→b | `ab` |
| 3 | a 的并发子元素 b(2,A)、x(1,B)，lamport 大者近 a | `abx` |
| 4 | b→c（因果，c 永随 b） | `abcx` |
| 5 | x→y | `abcxy` |
| 6 | a 墓碑：跳过 a，子孙保留 | `bcxy` |
| 7 | a 的子元素 z(3,B)、b(2,A)、x(1,B) | `zbcxy` |
| 8 | b 墓碑：跳过 b，子孙 c 保留 | `zcxy` |

- (甲) 第 3 步应为 `abx`；若按 lamport 升序则 x(1) 在 b(2) 前，错成 `axb`。
- (乙) 第 6 步后为 `bcxy`；物理移除会使第 7 步因 prev=(1,A) 不存在而整体失败、z 不入，错成 `bcxy`（正确 `zbcxy`）。
- (丙) 正确为 z 在 b 前（lamport 3>2）；丢 lamport 只按 replica 名排则 A 组在前，b 在 z 前，第 7 步错成 `bcxyz`，第 8 步错成 `cxyz`（正确 `zcxy`）。

## 2. 四条不变量：保证位置与钉住测试

1. 与朴素参照一致：`doc.Text()` 经 `rga` 有序树遍历（rga/rga.go 的 `visible`）；`api.SelfCheck` 用独立操作日志批量重算逐步比对。钉住：`api.TestSelfCheck`。
2. 收敛/因果不重叠：因果顺序由父子引用确定，并发同 prev 由 `rga.Less`（lamport 降序、replica 降序）全序确定，插入即落定序位置（rga/rga.go `place`）。钉住：`api.TestConvergence`、`api.TestEightStepTrace`。
3. 墓碑：`Element.Del` 只置位，节点与子链保留（rga/rga.go `Delete`/`visible`），被删元素仍可作 prev。钉住：`api.TestTombstone`。
4. 失败不留痕：`doc.Doc.Insert`/`Delete` 先全部校验（非法/重复/prev 不存在）通过后才改结构（doc/doc.go）。钉住：`api.TestRejectedOpsLeaveNoTrace`。
