# FINDINGS — 测试结论记录

## 表 ②：截断点分类（文件 `greeting = hello ${name}\n`，全长 25 字节）

逐字节截断到 n（1 ≤ n ≤ 24），全部 24 个截断点均报可判定错误，行号均为 1：

| 字节区间 n | 末行形态 | 分类结论 | 行号 |
|---|---|---|---|
| 1–9 | `greeting`（无 `=`） | ErrKeyIncomplete（键不完整） | 1 |
| 10–11 | `greeting =`（`=` 后无值字节） | ErrValueIncomplete（值不完整） | 1 |
| 12–24 | `greeting = h…`（部分值） | ErrLineIncomplete（行不完整） | 1 |

多行文件 `a = 1\nb = 2\n` 验证行号：截到 7（`b`）→ 键不完整 line 2；
截到 9（`b =`）→ 值不完整 line 2；截到 11（`b = 2`）→ 行不完整 line 2。
完整文件（含结尾换行）解析无错误；`a =\n` 是合法空值，与键不存在可区分。

## 表 ①：四层溯源输出（键 `greeting` 在四层都有值）

各层原始值（按优先级从低到高）与最终结果：

| 层 | 提供的原始值 | 是否最终来源 |
|---|---|---|
| default | `hi ${name}` | 否（被覆盖） |
| file | `hello ${name}` | 否（被覆盖） |
| env | `hey ${name}` | 否（被覆盖） |
| args | `yo ${name}` | 是 |

- 最终来源层：args；原始值 `yo ${name}`；展开值 `yo env`（`name` 最终来自 env 层）。
- 报告行（逐字节确定）：`greeting: source=args raw="yo ${name}" expanded="yo env" history=[default:"hi ${name}" file:"hello ${name}" env:"hey ${name}" args:"yo ${name}"]`
- 含引用键的原始/展开对（file 层 `greeting = hello ${name}`、env 层 `name = env`）：
  原始值 `hello ${name}`，展开值 `hello env`，两者同时出现在报告中。
- 只在默认值层出现的键 `only`：溯源仅 1 层（default），raw 与 expanded 均为 `o`。
- 打乱四层来源构造顺序 20 次，报告输出逐字节一致（键字典序、层序固定）。
