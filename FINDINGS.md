# 测试结论（FINDINGS）

## 表①：五种请求形态 —— 执行步骤序列与临时名数
| 形态 | 请求 | 执行步骤序列 | 临时名数 |
| --- | --- | --- | --- |
| 链 | `{a->b,b->c}` | 待 plan | - |
| 二元环 | `{a->b,b->a}` | 待 cycle | 待 cycle |
| 三元环 | `{a->b,b->c,c->a}` | 待 cycle | 待 cycle |
| 目标已存在 | `{a->x}`，x 已在 | 拒绝（ErrTargetExists），零步骤 | 0 |
| 同名冲突 | 重复 old / 重复 new | 拒绝（ErrDupSource/ErrDupTarget），零步骤 | 0 |

## 表②：日志截断点 —— 字节区间 / 分类 / 可撤销步数
待 apply/undo（日志头 7 字节；记录 `BE32(len)+payload+BE32(crc)`）。

## 包级结论
- name（已完成）：空串合法、`/`/`\` 普通字符、NUL 非法（`ErrInvalidName`）；Add 重复/Remove 不存在均不改集合；快照排序，集合相等与插入顺序无关。
