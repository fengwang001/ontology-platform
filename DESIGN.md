# 设备分组策略下发服务 — 设计说明

## 包划分与取舍
1. `group`：设备、分组、成员关系的纯数据状态；内置分组 `*`（pr=−1）在构造时建立，删/改优先级/成员操作一律 ErrInvalid。
2. `policy`：每组一份整份替换的策略表；`Value` 用 `{set, val bool/string}` 两态表达普通值（含空串）与 Unset；求值器只依赖 group 的只读接口，逐键在含该键的分组中取 pr 最大、并列名字典序最小者。
3. `push`：编排层。持有 group/policy 状态、每设备 A/Pd/failures、全局 pushId 与任务结局表；单一互斥锁保证并发等价于某一串行序。

## 关键语义决定（含被放弃方案）
4. Unset 遮蔽而非回落：胜者为 Unset 时该键不进 E，不回落到次高分组——否则 Unset 无法表达"删除更低配置"，空串与 Unset 也无法区分（空串照常下发）。
5. E 回到已确认内容时撤销任务（Cancelled）而非继续挂起：Pd 存在当且仅当 E≠A，避免设备持有过期/重复下发。
6. 不跨撤销复用编号：4 撤销后再变更占 5，pushId 单调连续无洞，结局表可精确复放；只有实际建任务占号，同次变更按设备名字节序发号。
7. 原子 Apply：先深拷贝状态，按序模拟全部操作，失败返回最小下标错误且零修改（含 pushId 计数不变）；成功后用新状态，仅按批后状态对受影响设备对齐一次——批内先加后删回原样的设备不入受影响集合，中间态不重算、不占号。
8. 受影响集合：成员增删（成员仍存在）取所涉设备；SetPolicy/SetPriority 取该组成员（`*` 为全部设备）；删设备取该设备；AddDevice 取新设备；AddGroup/RemoveGroup 为空集（空组才能删）。因此 recomputed 只与成员数有关。
9. 拒绝优先级固定：ErrInvalid > ErrNotFound > ErrExists > ErrNotEmpty > ErrTooManyGroups，逐操作即时判定，所以 `[AddMember(g3), RemoveMember(g1)]` 与逆序结果不同。
10. Ack/Nack 的 pushId 不等于当前 Pd 编号（含无 Pd、已撤销/取代）统一 ErrStale；Nack 只累加失败计数，Pd 不动。

## 复现性与验证
11. 无随机、无时钟、无 map 遍历依赖：发号前对设备名排序；任务结局 Acked/Cancelled/Superseded 全量留档。
12. 验证：`go test ./... -race`；表驱动用例覆盖并列取名小、Unset 遮蔽、空串、撤销、不换号、批中间态、批失败零变化、拒绝次序、Gmax 与重放；随机差分 1500 组（设备总数 100/10000 两档 × 成员 5 对照 recomputed）与朴素全量模拟器逐操作比对 E、A、Pd、pushId、结局，日志打印输入/输出/判定依据。
