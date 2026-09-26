# NOTES

## 第三节：八行分步表（`let x = 3 in if x < 5 then x + 1 else 0`）

| # | 子表达式 | 类型 | 环境（新增绑定） |
|---|---|---|---|
| 1 | `3` | int | {} |
| 2 | `x`（条件左操作数） | int | {x:int} |
| 3 | `5` | int | {x:int} |
| 4 | `x < 5` | bool | {x:int} |
| 5 | `x + 1`（含叶子 `x`、`1`） | int | {x:int} |
| 6 | `0` | int | {x:int} |
| 7 | `if x<5 then x+1 else 0` | int（条件 bool，两分支均 int） | {x:int} |
| 8 | `let x = 3 in …` | int | {}（x 已出作用域） |

- (甲) 正确结果：报错（ErrIf，then/else 类型不一致）。只查条件、不查分支相等的错误实现会返回 then 分支类型 `int`。
- (乙) 正确结果：报错（ErrArithOperand，`<` 的操作数非 int）。不查 `<` 操作数、直接给 bool 的错误实现会返回 `bool`。
- (丙) 正确结果：报错（ErrUndeclared，z 未声明）。把未声明变量默认为 int 的错误实现会返回 `int`。

## 第二节：四条不变量落点

1. 与朴素参照一致：`check` 包内 `expand`/`subst` 对每个 let 做显式替换展开后逐节点重推（`api.SelfCheck` 内置同法对拍）；测试 `TestNaiveAgreement`。
2. let 作用域：`check.Infer` 的 `Let` 分支用 `env.Extend` 生成子环境、仅用于推导 `e2`，原环境不变；测试 `TestInferTable`（let 外引用 x 报未声明）。
3. 条件分支一致：`inferIf` 先验条件为 bool，再验两分支类型相等，任一不满足即 ErrIf；测试 `TestInferTable`。
4. 失败不留痕：所有错误路径返回非 nil error、不返回类型；`Extend` 不改原环境，无全局可变状态；测试 `TestFailureNoTrace`。
