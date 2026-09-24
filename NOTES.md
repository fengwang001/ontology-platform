# NOTES

## 八步推导（DFS：元素后接其子元素；同 prev 子元素按 (lamport, replica) 降序；墓碑跳过字符但仍递归子孙）

| 步 | 操作 | 可见文本 |
|---|---|---|
| 1 | A Insert(∅,(1,A),'a') | `a` |
| 2 | A Insert((1,A),(2,A),'b') | `ab` |
| 3 | B Insert((1,A),(1,B),'x')（与步2并发） | `abx` |
| 4 | A Insert((2,A),(3,A),'c') | `abcx` |
| 5 | B Insert((1,B),(2,B),'y') | `abcxy` |
| 6 | A Delete((1,A)) | `bcxy` |
| 7 | B Insert((1,A),(3,B),'z')（插到墓碑后） | `zbcxy` |
| 8 | B Delete((2,A)) | `zcxy` |

(甲) 步3后应为 `abx`：(2,A) lamport=2 > (1,B) 的 1，b 更靠近 prev。若误按 lamport **升序** → `axb`。
(乙) 步6后步7前应为 `bcxy`（a 跳过、两支子孙保留）。若**物理移除**，步7找不到 prev=(1,A) 报 ErrPrevNotFound，z 插不进，步7后错成 `bcxy`；正确 `zbcxy`。
(丙) 丢 lamport、只按 replica 名字排（字符串升序 A<B）：b(A) 排到 z(B) 前，步7后错成 `bcxyz`，步8删 b 后错成 `cxyz`；正确为步7 `zbcxy`、最终 `zcxy`（c 由引用关系永远挂在 b 之后）。

## 不变量在代码中的保证位置 / 钉住的测试

1. 与朴素参照一致：rga.go `Add` 保序插入 + `Visible` DFS；api.go `SelfCheck` 朴素重放比对。测试 `TestNaiveReference`。
2. 收敛/因果不重叠：rga.go `greaterID` 全序（仅同父并发子参与），因果顺序由父子引用固定。测试 `TestConvergencePermutations`、`TestEightSteps`。
3. 墓碑：rga.go `Element.dead` 仅置位、`Visible` 跳过字符仍递归 children；doc.go `Delete` 不摘节点。测试 `TestTombstone`。
4. 失败不留痕：doc.go `Insert`/`Delete` 全部校验（非法 ID→重复 ID→prev 定位）先于任何 map/树写入。测试 `TestErrorsDistinctAndAtomic`。

复杂度：doc.go 非导出 `lastProbe`（map 哈希定位，∅=0、命中=1），doc.go `SelfCheck` 多档 m 自验；测试 `TestPrevProbeConstant`（包内测试直读非导出字段，数值不经任何导出接口）。
