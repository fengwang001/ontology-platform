# NOTES

## 三、八步推导（maxLeft=8，L1..L4 即 id 1..4）

| 步 | 操作 | ref（含 NULL） | 保留视图（升序） |
|---|---|---|---|
| 1 | AddLeft(L1,"a") | — | {} |
| 2 | AddLeft(L2,"a") | — | {} |
| 3 | AddRight("a") | a=1 | {L1,L2} |
| 4 | AddLeft(L3,"b") | a=1 | {L1,L2} |
| 5 | AddRight("b") | a=1, b=1 | {L1,L2,L3} |
| 6 | AddLeft(L4,nil) | a=1, b=1 | {L1,L2,L3} |
| 7 | DelRight("a") | a=0, b=1 | {L3} |
| 8 | AddRight(nil) | a=0, b=1, NULL=1 | {L3} |

**(甲)** 第 3 步后再 `AddRight("a")`（ref 1→2），半连接视图不变仍 {L1,L2}。若错实现为内连接展开，左行总出现次数从 2 错成 4：L1、L2 各多出现 1 次（各出现 2 次）。
**(乙)** 第 8 步后 L4（NULL 键）**不**进视图。若把 NULL 当可匹配（NULL==NULL 或等同空串），ref[NULL]=1≥1 会点亮 L4，视图错成 {L3,L4}，多出 id L4。
**(丙)** `AddRight("a")` 提到最前：最终视图相同（{L3}）——不变量 1 的视图只是当前 left 与 ref 的函数，与到达顺序无关。差异在点亮时刻：原序 L1/L2 在第 3 步被 AddRight 点亮；新序 ref["a"] 已为 1，L1/L2 在各自 AddLeft 时即点亮。差异来自「保留随 ref 即时切换」规则：点亮由「左行到达」与「ref 0→1」中较晚者触发。参照必须按「ref≥1 的存在性、每行至多一次」判，因为半连接语义是存在即保留；若按「左×右展开」计数，视图会随 ref 大小出现重复行，违背不变量 2，也不再是集合。

## 二、不变量落实（位置 + 钉住它的测试）

1. **与批量重算一致**：`semi` 中 `retained` 集随每次操作增量维护，参照 `BatchView()` 同规则全量重算；`TestBatchConsistency`（随机序列逐步比对）。
2. **半连接不重复**：`retained` 是 `map[int64]struct{}` 集合，点亮/熄灭按 id 幂等；`TestNoDuplicate`（ref>1 时视图无重复）。
3. **撤回不越界 / NULL 不匹配**：`DelRight` 先查后减、为 0 即拒绝；nil 键不进 `byKey` 索引、永不点亮；`TestRefNonNegativeNullNoMatch`。
4. **失败不留痕**：每个公开操作先完成全部校验再写任何字段（见 `semi.go` 各方法开头的校验）；`TestFailureNoTrace`（四类拒绝前后状态逐字段一致）。
