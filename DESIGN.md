# 告警分组通知器设计说明

## 结构
- `alertstore`：指纹→告警记录（labels 快照、since、firing、deduped），活跃=firing∪未吸收 resolved；resolved 原地保留，复火不占名额。
- `suppress`：静默表（id→matcher/start/end）与抑制规则表；可见性 = firing 且未命中半开区间 [start,end) 静默且未被任一 firing 异指纹源抑制。
- `notify`：Notifier 持单把 `sync.Mutex`，内含 store、suppress、分组状态（lastSent/lastSet）与时钟 maxNow；Tick 持锁按组键序同步调 send。

## 关键取舍
- 错误优先级：参数非法 → 时钟回退 → 状态类（Resolve 非 firing / AddSilence 重复 / ExpireSilence 不存在 / Fire 超限）；校验全部通过才改任何字段（含 Deduped、时钟）。
- 抑制源只要求“指纹不同且 firing”：源自身被静默或被抑制仍有资格，抑制单层判定、不传递、不递归；互抑制时双方皆不可见。放弃“只认可见源/递归”，因规范明确且递归会引入不动点歧义。
- equal 比较取原始标签值，双侧缺失（空串）视为相等。
- 首次等待 W 只在条件 (a)（组无 lastSent）生效；变化通知走 (b) 不再等 W。放弃“变化也再等 W”，否则抑制解除等场景会延迟且与示例矛盾。
- 发送失败：该组 lastSent/lastSet 不变，D 与“本应直接清除的 resolved”全部保留；未调用 send 的组才在本次清除 resolved。下次 Tick 用同一份状态重新计算，通知内容逐字节一致。
- 组内无任何告警记录才删除组状态；此后新告警重新按 (a) 等待 W。
- 满 Amax 直接拒绝新建，不驱逐。放弃 LRU/驱逐旧告警：会丢失未通知的 resolved、破坏可复现性与上限保证。
- resolved 未在 lastSet 中者：从未成功通知过，直接静默清除，不产生 Resolved。
- 时钟由 Notifier 统一维护单调 maxNow；store/suppress 自身不校验时钟，便于独立使用与测试。
- 单锁简化并发，send 在锁内同步调用（禁止回调本对象），获得严格串行等价语义与确定性重放。
- 指纹/分组键为确定性格式：指纹按键字节序 `k=v` 逗号连接；分组键按 G 顺序取值（缺失空串）以 `\x00` 连接。
