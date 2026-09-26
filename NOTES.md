# NOTES

## 第三节：五个 delta 逐步推导

| 步 | delta | 判定 | Version() | State() |
|---|---|---|---|---|
| D1 | From=0 To=1 [Set a=1] | From==version，应用 | 1 | {a:1} |
| D2 | From=1 To=2 [Set b=2] | From==version，应用 | 2 | {a:1, b:2} |
| D3 | From=2 To=3 [Set c=3, Del b] | From==version，按序应用 | 3 | {a:1, c:3} |
| D4 | From=3 To=4 [Set d=4] | From==version，应用 | 4 | {a:1, c:3, d:4} |
| D5 | From=0 To=1 [Set a=100] | From=0<4，重复，跳过 | 4 | {a:1, c:3, d:4} |

- (甲) D5 是重复旧 delta，正确实现跳过，`a` 最终为 1。若见 delta 就应用，`a` 会错成 100（正确为 1）。
- (乙) D3 的 `Del b` 生效，`b` 最终不存在。若只应用 Set 丢弃 Del，`b` 会错留为 2（正确为不存在）。
- (丙) 若把「已应用」误写成 `From <= version`，D1（From=0<=version=0）会被当重复跳过，`a` 会错成不存在（正确为 1）。

## 第二节：四条不变量的保证位置与钉住它的测试

1. 与顺序重放一致：`replica.Apply` 只在 `From==version` 时按序回放 Change（`delta.ApplyChange`），测试 `TestMatchesNaiveReplay`。
2. 版本单调：`replica.Apply` 仅在成功应用后赋 `version=d.To`（d.To>d.From 已校验），测试 `TestVersionMonotonic`。
3. 幂等去重：`replica.Apply` 中 `From<version` 分支直接返回 nil 不动状态，测试 `TestDuplicateIdempotent`。
4. 失败不留痕：`replica.Apply` 先校验（负版本/To<=From/空 key/gap）再改任何字段，测试 `TestRejectedKeepsState`。
