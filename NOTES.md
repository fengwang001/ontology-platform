# NOTES — JSON Patch 推导与不变量

## Diff(A,B) 补丁表（顶层键字节序：a/b < arr < k < m~n < n < z < ~1）

| # | op | path | value | 来源 |
|---|---|---|---|---|
| 1 | replace | /a~1b | 2 | 键 a/b：两侧都有、标量不等 → replace |
| 2 | replace | /arr | [5] | 键 arr：数组一律整体 replace |
| 3 | add | /k | {"q":1} | 键 k：仅 B 有 → add |
| 4 | add | /m~0n/w | [] | 键 m~n 递归：w 仅 B 有 → add |
| 5 | remove | /m~0n/x | — | 键 m~n 递归：x 仅 A 有 → remove |
| 6 | replace | /m~0n/y | 3 | 键 m~n 递归：y 不等 → replace |
| 7 | add | /n | null | 键 n：仅 B 有（null 是存在的值）→ add |
| 8 | remove | /z | — | 键 z：仅 A 有 → remove |
| 9 | replace | /~01 | false | 键 ~1：两侧都有、bool 不等 → replace |

## 三问

- (甲) `a/b`→`/a~1b`，`~1`→`/~01`。编码写反（先 /→~1 再 ~→~0）：`a/b` 错成 `a~01b`，正确应用端解码得键 `a~1b`，A 中不存在 → 第 1 条失败，路径不存在。解码写反（先 ~0→~ 再 ~1→/）：其余条正常，`/~01` 被解成键 `/`（~0→~ 得 `~1`，再 ~1→/ 得 `/`）→ 第 9 条失败，路径不存在。
- (乙) 逐元素差分 arr 生成 `remove /arr/1`、`/arr/2`、`/arr/3`（升序）。应用到 A：第 1 条后 [5,7,8]，第 2 条后 [5,7]，第 3 条下标 3 ≥ len 2 → arr 组内第 3 条（全补丁第 4 条）失败，路径不存在；失败前 arr=[5,7]（原子性保证调用方的 A 不变）。
- (丙) 少第 7 条 `add /n null` 与第 8 条 `remove /z`；结果比 B 多 `"z":null`、少 `"n":null`。正确补丁应用到 B：第 5 条 `remove /m~0n/x` 时 B 的 m~n 无 x → 路径不存在，整体失败；原子性 → 调用方手中 B 与应用前深度相同。

## 不变量（保证位置 / 钉住测试）

1. 往返相等：patch.Diff 递归 + patch.Apply 深拷贝后逐条应用 → `TestRoundTrip`。
2. 最小且有序：diff 仅在叶子/数组/异型产一条、键并集字节序 → `TestMinimalOrdered`。
3. 输入不改：Apply 先 deepCopy、Diff 只读 → `TestNoMutation`。
4. 失败不留痕：错误只发生在深拷贝上、原件不返回；四类哨兵错误 → `TestFailureAtomic`、`TestErrorKinds`。
