# 确定性重放校验器：设计说明

三个包：`history`（追加式事件日志）、`code`（工作流代码描述）、`replay`（重放/续跑）。

## 核心取舍

Branch 走新分支 N 还是旧分支 O，**只依据运行时两个事实**：历史是否耗尽
（c 与快照长度 L 的关系）；未耗尽时下一事件是否正是本补丁标记 M(pid)。
规则：pid∈P 或下一事件是 M(pid) → N（后者消费标记、加入 P）；是 M(q)（q≠pid）→
ErrUnexpectedMarker；是 S → 走 O（不加入 P）；历史耗尽 → 生成 M(pid)、加入 P、走 N。

## 为什么不给事件打代码版本号

- 旧历史由旧代码产生，没有版本字段；版本号比较要求升级时先迁移/回填历史。
- 补丁标记自描述：已打补丁的历史自带 M(pid)，未打标记的旧前缀遇 Branch 时下一事件
  必是 Step，据此即可走旧分支 O，无需额外元数据。
- 代价（预期语义）：旧历史恰在 Branch 前被截断时，历史看似耗尽，续跑改走 N 并补写
  M(pid)，完整历史与旧代码完整历史不同。

## 被放弃的方案

- 版本号比较：需迁移无版本可填的旧历史，且多补丁与版本对应关系复杂。
- 静默忽略多余标记：跳过 M(q) 会使后续 Step 与历史整体错位、吞掉确定性偏差，
  故一律报 ErrUnexpectedMarker。

## 执行与错误

Run 先取一次 Snapshot（长度 L），随后纯本地模拟：Step 按 c 比对或生成；
Branch 展开只含 Step 的 N/O（不嵌套由 code 包强制）。
三类非确定性错误（ErrMismatch / ErrUnexpectedMarker / ErrHistoryExtra）按遇到先后报第一个，
且不追加；成功且续跑非空时仅一次 Append(wf, L, 新事件)，
ErrConflict/ErrCapacity 上抛且不留事件。
拒绝优先级：参数非法与 ErrCode → 执行期非确定性错误 → 追加期 ErrConflict、ErrCapacity。
空批次 Append 为成功无操作（不校验参数）。

## 本地验证

`go build ./... && go vet ./... && gofmt -l .`；`go test ./...`；
`go test -race ./replay`（同工作流并发恰一次真正追加，其余 ErrConflict，重试收敛）；
`go test -run TestNaive -v ./replay`（随机代码×随机历史含非法历史，对照朴素逐步模拟并打印判定）。
