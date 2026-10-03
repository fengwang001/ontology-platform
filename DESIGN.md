# 设计说明：带权限交集与人工放行的工作流运行器

## 模型
- grants：主体（非空 []byte）→ uint64 掩码，位 63 为审批位，位 0–62 为业务位。Grant 做 `|=`，Revoke 做 `&^=`。
- flow：定义含上限 ceil（不含位 63）与 1–16 个非零且为 ceil 子集的需求掩码。
- runner：实例、审计日志、单调全局时钟（初值 0），挂起时限 T。

## 核心取舍
- 有效权限 eff = grants(p) 实时值 & 启动快照 E（E = grants(p)&ceil 于 Launch 时刻）。
  启动后权限“只缩不涨”：实时撤销立即生效，新增授权被快照封顶。
- 放弃方案 A「整体按启动快照」：撤权滞后，在途步骤仍可使用已被收回的权限。
- 放弃方案 B「整体实时（无快照）」：运行中提权会让在途流程获得启动时未经审查的权限。
- 已 Running 的步骤不被撤权打断：放行决策只在步骤边界（StartStep）发生；
  中途撤权在下一步 StartStep 拦截，保证步骤执行期间决策稳定、终局可复现。
- Override 只覆盖当前一步：审批是针对“该步骤缺哪些位”的一次性例外；
  后续步骤仍按 eff 判定，避免一次放行静默放大整个工作流权限。

## 时钟与到期
- 所有带 now 的操作：先校验参数，再校验 now∈[0,1e15] 且 now≥全局时钟；成功才推进时钟。
- 每个操作先做到期处理再判定本体；Suspended 且 deadline≤now（恰等即到期）→
  Failed(Expired)，终局时刻 = deadline，审计写 Expire。Status 为只读，按 now 虚拟投影。
- 因“到期处理后已 Failed 上 Approve 须 ErrState（先于 ErrSelf/ErrNoAuthority）”，
  到期发生在一次被拒绝的调用中时仍提交 Expire：到期是 now 的纯函数，可被任何调用者
  在该时刻观测；被拒绝的本体不写任何额外审计、不改其余状态。

## 并发与审计
- 单一互斥锁串行化全部变更，故交错结果等价于某一串行顺序；StartStep 对 grants 恰读一次。
- 审计每实例序号自 1 连续；Allow/Deny/Override/Reject/Expire 每个成功状态变更恰一条；
  终局（Completed/Failed）唯一且之后不变。

## 本地验证
- `go test ./...`；`go test -race ./...`（并发交错）；`go test -v` 查看逐步输入输出与判定依据；
  随机用例与逐步朴素模拟（位运算逐位核对）对照。
