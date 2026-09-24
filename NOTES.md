# 折叠口径推导与不变量落实

## 折叠口径推导（第三节）

| 输入 | 逐 rune（SimpleFold 轨道取小写不动点） | 完整折叠（CaseFolding） |
|---|---|---|
| `STRASSE` | `strasse` | `strasse` |
| `strasse` | `strasse` | `strasse` |
| `straße` | `straße`（ß 的轨道为 {ß, ẞ}，最小是 ß，不变） | `strasse`（ß→ss，变长） |
| `İ` (U+0130) | `İ`（轨道仅自身，不变） | `i̇`（i + U+0307，变长） |

完整折叠会破坏的两条不变量：
- 不变量 1（幂等）：ß→ss、İ→i+◌̇ 使输出字符集不同于输入，幂等不再由构造保证，需额外验证；变长展开还可能把折叠键撑过键长上限。
- 不变量 3（原始键保真）：折叠改变长度，"折叠键"与"原始写法"长度不再一致，键长上限该对哪种形态生效变得含糊，保真只能靠额外存原始键补救。

选择：逐 rune 映射（取 `unicode.SimpleFold` 轨道内的小写不动点，无则取轨道最小值）。长度不变、幂等由构造保证、与 `strings.EqualFold` 同口径。此时 `"straße"` 与 `"STRASSE"` 折叠结果不同、互不命中；这不违反不变量 2——它只要求 Fold 相等则命中一致，是充分条件而非语言等价。

## 五条不变量落实

1. 折叠幂等：`fold/fold.go` 的 `Rune` 取轨道内小写不动点，再折结果不变 → `TestFoldIdempotent`
2. 命中一致：`keymap/keymap.go` 的 `Put`/`Get` 都按 `fold.Fold(key)` 索引 → `TestHitConsistency`
3. 原始键保真：`keymap.Put` 命中已有折叠键时只覆盖 `Value` → `TestKeysFidelityAndOrder`
4. 顺序确定：`keymap.Keys` 对折叠键 `sort.Strings` 后取原始键 → `TestKeysFidelityAndOrder`
5. 失败不留痕：`keymap.Put` 先校验（空/超长/超量）后加锁写入 → `TestRejectLeavesNoTrace`

自检：`keymap.SelfCheck` → `TestSelfCheck`；并发只读：`TestConcurrentReads`。
