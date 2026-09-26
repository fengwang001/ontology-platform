# NOTES

## 八行分步表：`let x = 3 in if x < 5 then x + 1 else 0`
| # | 检查的子表达式 | 推断类型 | 当前环境（新增绑定） |
|---|---|---|---|
| 1 | let 的 e1：`3` | int | ∅ |
| 2 | 绑定 x，进入 in 作用域 | — | {x:int} |
| 3 | 条件里查变量 `x` | int | {x:int} |
| 4 | `5` → int；故 `x < 5` | bool | {x:int} |
| 5 | then 里查变量 `x` | int | {x:int} |
| 6 | `1` → int；故 `x + 1` | int | {x:int} |
| 7 | else `0` → int，与 then 相同 | int | {x:int} |
| 8 | `if` → int；`let` 整体 → int，退出后 x 失效 | int | ∅ |

- **(甲)** `if true then 1 else true`：正确=**报错**（then:int、else:bool 不一致）；只查条件不查分支相等的错误实现会返回 **int**（then 分支类型）。
- **(乙)** `1 < true`：正确=**报错**（`<` 要求两个 int）；不查操作数类型的错误实现会返回 **bool**。
- **(丙)** `z + 1`（空环境）：正确=**报错**（z 未声明）；未声明变量默认 int 的错误实现会返回 **int**。

## 第二节四条不变量：代码位置 / 钉住的测试
1. 与朴素参照一致：`check.checker.infer` 与 `check.naiveRef` 共用纯规则表 `unaryRule`/`binaryRule`，无子类型无隐式转换；测试 `TestReferenceEquivalence`（200 个随机表达式，参照对每个 let 做显式复制+按名替换展开）。
2. let 作用域正确：`check.go` 的 KLet 用 `env.child()` 派生只含 x 的不可变新帧，`lookup` 沿 parent 向外查，调用方原 Env 拿不到 x；测试 `TestLetScope`。
3. 条件分支一致：`check.go` 的 KIf 先验条件为 bool，再验 then/else 用 `types.T.Equal` 相等；测试 `TestIfBranches`。
4. 失败不留痕：`check.Infer` 从不在调用方传入的 Env 上写入（绑定只进新建子帧），无全局状态、checker 每次新建；测试 `TestFailureLeavesNoTrace`。
