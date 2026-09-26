# NOTES

## 一、`8/2/2-3` 后序遍历构建 AST（共 7 节点）

| 步 | 构造节点 | 运算/字面量 | 左操作数值 | 右操作数值 | 本节点值 |
|---|---|---|---|---|---|
| 1 | N1 | 字面量 8 | 无 | 无 | 8 |
| 2 | N2 | 字面量 2 | 无 | 无 | 2 |
| 3 | N3 = N1 / N2 | `/` | 8 | 2 | 4 |
| 4 | N4 | 字面量 2 | 无 | 无 | 2 |
| 5 | N5 = N3 / N4 | `/`（左结合） | 4 | 2 | 2 |
| 6 | N6 | 字面量 3 | 无 | 无 | 3 |
| 7 | N7 = N5 - N6 | `-` | 2 | 3 | -1 |

- **(甲)** 若 term 循环错为右结合（`8/(2/2)`）：第 3 步先算右边 `2/2=1`，第 5 步再算 `8/1=8`，最终 `8-3=**5**`（正确为 -1）。
- **(乙)** 若四运算符同级、严格从左到右：`(2+3)*4=5*4=**20**`（正确为 14）。
- **(丙)** `-7/2` 若用 floor：`floor(-3.5)=**-4**`（向零截断应为 -3）。

## 二、四条不变量：保证位置与钉测

1. 与两栈参照一致：独立算符优先参照实现 `referenceEval`（api/api.go，优先级 1/2/3）；钉测 `TestReferenceRandom`、`TestSelfCheck`。
2. AST/求值自洽：`api.Eval` 纯递归 `*parse.Node`，不接触记号流；钉测 `TestParseEvalConsistency`。
3. 左结合：`parse.(*parser).expr/term` 经 `fold` 左折叠循环（`left = bin(left, op, right)`）；钉测 `TestLeftAssoc`（与参照对拍）。
4. 失败不留痕：三包均无包级可变状态（哨兵只读、解析器按次新建），错误整体返回；钉测 `TestNoStateAfterReject`。

LL(1) 前瞻峰值：非导出字段 `parser.buf`/`parser.peak`（parse/parse.go），包内测试 `TestLookaheadPeak` 直读，公开侧只经 `parse.VerifyLL1` 得布尔判定、不读数值。
