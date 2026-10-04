# 来料质检计数抽样放行器 — 设计说明

## 结构
- `plan`：不可变抽样方案表。闭区间 [lo,hi] 互不重叠；每档校验 n≥1、0≤Ac<Re，Normal/Tightened 要求 Re=Ac+1，Reduced 允许 Ac+1<Re（边缘接收带）；Lr≥0。
- `switchrule`：每流一个状态机，只看初检批。Severity 四态 Normal/Tightened/Reduced/Suspended；`Record{d, rejected, borderline, accepted}` 喂入。
- `lot`：Inspector 门面。全局互斥锁 + 每流状态、全局批号表；批上快照提交时刻的 (n,Ac,Re)（n 已按 N 截断），复检轮改存 Tightened 快照。

## 转移规则取舍
- Normal：仅保留进入 Normal 以来最多最近 10 条初检记录（环形切片）。判定后一次扫描：最近 5 条中拒收≥2 → Tightened；否则若已≥10 条、最近 10 条全接收且 d 和≤Lr → Reduced。两条规则互斥（拒收批使“全接收”不成立），顺序固定。
- Tightened：维护连续接收数与累计拒收数（均 O(1)，不回看历史，looked=0）。连续接收达 5 → Normal（优先）；累计拒收达 5 → Suspended。
- Reduced：本批拒收或边缘接收即回 Normal。
- Suspended：Submit 拒绝；Resume（Manager）清状态进 Tightened；Rejected 批仍可 Resubmit。

## 被放弃的方案
- 切换后沿用历史窗口：放弃。规则明确“自进入以来”，进入前记录不带入（例：Tightened→Normal 首批拒收不转），故每次切换清空全部记录与计数。
- 复检批计入转移：放弃。复检固定 Tightened 档，结果只改批终态（Released/Scrapped），不喂状态机，故暂停期间也能复检且 looked 不增加。
- 按提交序排队多批：放弃。每流至多一个未判定批（含复检轮），冲突直接报状态不符，语义简单且可复现。
- 保存全量历史逐条回看：放弃于生产路径，改有界窗口（≤10 条）；测试中用保存全量历史的朴素模拟做 1500 组随机对照。

## looked 证明
- 非导出 `looked` 为每次 Observe 实际扫描的历史记录条数，Normal 最多 10、其余为 0，与流历史长度无关；测试以历史 100 与 10000 批两档对照断言。

## 拒绝次序（只报第一个）
参数非法 > 无权限 > 不存在 > 流已暂停 > 状态不符 > 冲突 > 数量越界；被拒绝操作不改任何状态（先校验后落盘）。

## 复现与并发
- 每批方案 = 提交时刻流严格度对应方案，快照在批上；转移在初检 Record 内同锁完成，对下一批生效。
- Inspector 单锁串行化全部操作，结果等价于某串行顺序；不同流在锁内仍互不影响。相同操作序列重放得相同轨迹。

## 本地验证
- `go build ./...`；`go vet ./...`；`gofmt -l .`
- `go test -race ./...`：表驱动用例（Ac/Re 边界、5 窗滑动 vs 累计、切换清空、10 窗与 Lr 取等、边缘、加严 5 拒收暂停/恢复、复检不计数、暂停期复检、n>N、拒绝次序、looked 100/10000 对照）+ 1500 组随机序列与朴素模拟对照，t.Logf 打印输入/输出/判定依据。
