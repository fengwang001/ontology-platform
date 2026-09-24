# NOTES

## 第三节推导：Diff(A, B) 补丁表（顶层键按字节序：a/b < arr < k < m~n < n < z < ~1）

| # | op | 路径(编码后) | 值 | 来源规则 |
|---|---|---|---|---|
| 1 | replace | /a~1b | 2 | 键 a/b 两边都有，标量不等 → replace |
| 2 | replace | /arr | [5] | 键 arr 两边都是数组 → 整体 replace |
| 3 | add | /k | {"q":1} | 键 k 只在 B → add |
| 4 | add | /m~0n/w | [] | m~n 递归：键 w 只在 B → add |
| 5 | remove | /m~0n/x | — | m~n 递归：键 x 只在 A → remove |
| 6 | replace | /m~0n/y | 3 | m~n 递归：y 两边都有且不等 → replace |
| 7 | add | /n | null | 键 n 只在 B（值为 null 也是存在）→ add |
| 8 | remove | /z | — | 键 z 只在 A → remove |
| 9 | replace | /~01 | false | 键 ~1 两边都有，bool 不等 → replace |

(甲) 键 a/b → `/a~1b`；键 ~1 → `/~01`。编码顺序写反（先 /→~1 再 ~→~0）：a/b 错成 `a~01b`，正确应用端把它解码成键 `a~1b`，A 中不存在 → 第 1 条即失败，错误类别「路径不存在」。解码顺序写反（先 ~0→~ 再 ~1→/）：正确补丁第 9 条 `/~01` 被解码成键 `/`（~01→~1→/），A 中无此键 → 第 9 条失败，「路径不存在」。
(乙) 逐元素差分生成：remove /arr/1、remove /arr/2、remove /arr/3（全补丁第 2~4 条）。应用到 A：第 2 条后 arr=[5,7,8]，第 3 条后 arr=[5,7]，第 4 条 remove /arr/3 时下标 3 ≥ len 2 → 第 4 条失败，「路径不存在（下标越界）」，失败前 arr=[5,7]。
(丙) 把 null 当不存在：少第 7 条 add /n null 与第 8 条 remove /z；应用到 A 的结果比 B 多出 "z":null、缺少 "n":null。正确补丁应用到 B：第 5 条 remove /m~0n/x 时 B 的 m~n 无键 x → 第 5 条失败，「路径不存在」；应用是原子的，调用方手里的 B 与应用前完全相同。

## 四条不变量的保证位置与钉住测试

1. 往返相等：patch.Diff 递归生成 + patch.Apply 在克隆上逐条 applyAt（patch/patch.go）；测试 TestRoundTrip、TestSection3。
2. 最小且有序：diff 按键并集字节序递归、数组整体 replace（patch/patch.go diff）；测试 TestDiffMinimalOrdered（朴素计数 naive 对照 + 路径递增无前缀）。
3. 输入不被修改：Apply 先 ptr.Clone 再改、Diff/Apply 写值前 ptr.Clone（ptr/ptr.go Clone）；测试 TestNoMutation。
4. 失败不留痕：Apply 只在副本上改、任一失败整体返回错误，四类哨兵错误互不相同（ptr.ErrInvalidPath/ErrNotFound、patch.ErrInvalidOp/ErrTooManyOps）；测试 TestAtomicFailure。

定位复杂度：ptr.Step 每次只查一个键/下标并计数（ptr/ptr.go checks），测试 ptr 包内 TestLookupCount 钉住不随 m 增长。并发：TestConcurrentApply。
