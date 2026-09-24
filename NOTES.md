# NOTES

## 折叠口径推导（第三节）

| 输入 | 逐 rune（SimpleFold 轨道最小值） | 完整折叠（toCasefold） |
|---|---|---|
| `STRASSE` | `STRASSE` | `strasse` |
| `strasse` | `STRASSE` | `strasse` |
| `straße` | `STRAßE` | `strasse`（ß→ss，变长） |
| `İ`(U+0130) | `İ` | `i̇`(U+0069 U+0307，变长) |

逐 rune 列由 `strings.Map(轨道最小值)` 实测；完整折叠列依 Unicode CaseFolding.txt（00DF→0073 0073，0130→0069 0307）。

完整折叠会破坏的不变量：
- 不变量 1（幂等）：完整折叠改变 rune 数，`Fold("ß")="ss"` 后必须再保证 `Fold("ss")="ss"`，`"İ"`→`"i̇"` 含组合符再折须原样不动——幂等不再免费。标准库没有完整折叠（`strings.EqualFold("ß","ss")` 为 false），须自带展开表并证明映射对自身像封闭；任何「先 ToUpper 再 ToLower」式组合直接翻车（`ToUpper("ß")="ß"`，根本到不了 `ss`）。
- 不变量 3（保真）：折叠后长度与内容都偏离原始键（`ß`→`ss` 多出一个 rune），折叠键绝不能再兼任原始键，必须另行单独保存首次插入的原始写法。

选择：逐 rune 简单折叠（SimpleFold 轨道最小值）。理由：标准库直接支持；rune 数守恒；轨道最小值是不动点，幂等可证（已对 0..MaxRune 全量验证）；且 `Fold(a)==Fold(b)` 当且仅当 `strings.EqualFold(a,b)`。此口径下 `Fold("straße")="STRAßE"` 不等于 `Fold("STRASSE")="STRASSE"`，二者互不命中——不违反不变量 2：不变量 2 只要求「折叠相等⇒同记录、不等⇒互不命中」，与 EqualFold 语义一致。

## 不变量落实

1. 幂等：`fold.Rune` 取 SimpleFold 轨道最小值（fold/fold.go）— TestIdempotent
2. 命中一致：`keymap.Map` 以 `fold.String(key)` 为唯一索引（keymap/keymap.go）— TestHitConsistency
3. 原始键保真：`entry.orig` 仅首次插入时写入，重复 Put 只覆盖值（keymap/keymap.go Put）— TestOrigKeyPreserved
4. 顺序确定：`Keys` 对折叠键 `sort.Strings` 后输出原始键（keymap/keymap.go Keys）— TestKeysOrderDeterministic
5. 失败不留痕：`Put` 先校验空键/键长/表项数全部通过才写 map（keymap/keymap.go Put）— TestRejectedOps

自检：`Map.SelfCheck` 核验每项折叠键==fold(原始键)、无两项共享折叠键、表项数与记录数一致；单遍扫描计数器 `lastRunes`（非导出）由 TestSinglePass 钉住；并发只读一致性由 TestConcurrentReads 钉住。
