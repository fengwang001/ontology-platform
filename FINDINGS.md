# FINDINGS

## 表 ① 四层溯源输出（键 `who` 与 `greeting`）

场景：default `who=d0 greeting="hi ${name}" name=def`；file `who=f0 greeting="hello ${name}" name=file`；
env `WHO=e0 NAME=env`；cli `--who=c0`。

| 键 | default | file | env | cli | 最终值（层） | 原始值 | 展开值 |
|---|---|---|---|---|---|---|---|
| who | d0 | f0 | e0 | c0 | c0（cli） | c0 | c0 |
| greeting | hi ${name} | hello ${name} | — | — | hello ${name}（file） | hello ${name} | hello env |

报告原文（按键字典序，覆盖层从低到高列出）：

```
key=greeting final=file:"hello ${name}" overridden=[default:"hi ${name}"] expanded="hello env"
key=who final=cli:"c0" overridden=[default:"d0",file:"f0",env:"e0"] expanded="c0"
```

结论：溯源在展开前记录原始值，同一键同时给出原始值与展开值；四层齐全时顺序为
default→file→env→cli；只在默认值层出现的键（如 `only`）溯源仅一层。

## 表 ② 截断点分类（样本文件 `name = file\ngreeting = hello ${name}\n.\n`，共 39 字节）

逐字节截断 1..38，每个切点都产生可判定错误并带行号，分类区间如下：

| 切点区间（字节） | 分类 | 报出行号 |
|---|---|---|
| 1–5 | 键不完整（末行无 `=`） | 1 |
| 6–11 | 值不完整（末行已含 `=`） | 1 |
| 12 | 行不完整（止于换行、无终止行） | 2 |
| 13–20 | 键不完整 | 2 |
| 21–36 | 值不完整 | 2 |
| 37 | 行不完整 | 3 |
| 38 | 键不完整（终止行 `.` 被切） | 3 |

结论：38 个切点全部落入三类之一，无静默成功；三类均可由 `errors.Is` 区分
（`ErrLineIncomplete` / `ErrKeyIncomplete` / `ErrValueIncomplete`）。
