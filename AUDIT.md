# 不变量审计

## 不变量 1：老句柄绝不复活
- 保证位置：`table/table.go` 的 `Remove`——代号只递进不回绕，达
  `handle.MaxGen` 的槽位置 `Exhausted` 退役、永不再入空闲链表；
  `lookup` 要求 `InUse && Gen 相等` 才放行。
- 钉住测试：`table.TestStaleNeverRevivesAcrossReuse`（复用 500 次后老句柄
  仍失效）、`table.TestExhaustion`（耗尽槽位的句柄为 ErrStaleHandle）、
  `audit.TestABAStaleHandleNeverSucceeds`（并发 ABA 下零次成功）。

## 不变量 2：句柄不跨表
- 保证位置：`table/table.go` 的 `tagCounter` 为每张表分配唯一 tag 并编入
  句柄高 16 位；`lookup` 比较 `h.Tag() != t.tag` 即判 `ErrForeignHandle`。
- 钉住测试：`table.TestHandleErrors` 的 `foreign` 用例。

## 不变量 3：存活集合自洽
- 保证位置：`table/table.go` 的 `live`/`dead` 计数与 `freelist.List.n`
  同步增减；`audit/audit.go` 的 `Check` 核验
  `Len + 空闲数 + 耗尽数 == Cap`、空闲链表无在用/重复槽位、在用槽位
  代号与其句柄一致。
- 钉住测试：`table.TestInvariantEquation`（2000 步随机操作每步核验）、
  `audit.TestCheckAndStats`、`audit.TestABAStaleHandleNeverSucceeds`
  （并发结束后 Check 为 nil 且等式成立）。

## 不变量 4：失败不留痕
- 保证位置：`table/table.go` 的 `lookup` 先校验后变更，`Insert` 在
  `free.Take` 失败时不触碰任何字段；错误是当前状态的纯函数，故同一失效
  句柄连用两次返回同一 `ErrStaleHandle`（DESIGN.md 推导四）。
- 钉住测试：`table.TestFullInsertKeepsState`（被拒 Insert 前后 Slots 与
  FreeIndices 逐字段相同）、`table.TestHandleErrors`（被拒 Get/Remove
  后 Len 不变、`stale-second-use` 错误相同）。

## 不变量 5：零值句柄永不有效
- 保证位置：`handle/handle.go` 的 `Zero`；`table/table.go` 的 `New` 使
  tag 从 1 开始、各槽位代号从 1 开始，全 0 组合永不发出；`lookup` 先判
  `IsZero` 返回 `ErrZeroHandle`。
- 钉住测试：`table.TestHandleErrors` 的 `zero` 用例。

## 取空闲槽位的访问记录数（freelist 非导出计数器 lastVisits）
- 保证位置：`freelist/freelist.go` 的 `Take` 只触头结点，O(1) 不扫描。
- 钉住测试：`freelist.TestTakeVisitsScaling`（上限 4）。

| 容量 | 释放一半后取一个访问的槽位记录数 |
| ---: | ---: |
| 1000 | 1 |
| 100000 | 1 |

实测两档均为 1（≤4），不随容量增长。
