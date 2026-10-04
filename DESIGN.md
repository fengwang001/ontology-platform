# 差分隐私预算账本设计说明

## 取舍
1. 三个包：`budget`（无锁账目原语：数据集 Used/Resv、分析师窗口窗占用）、`plan`（计划树求值与结构校验）、`ledger`（状态机、时钟、预留/启动/结算/撤销、并发互斥）。
2. 并发：ledger 内单把 `sync.Mutex`，所有公共方法临界区串行化，天然等价于某一串行顺序；账目结构本身无锁，不重复加锁。
3. 时钟 `clock` 只随“被接受”的操作推进；被拒绝（含参数非法、时钟回退）一律不写任何状态，包括时钟。只读的 Remaining 不推进时钟，但 now < clock 仍报时钟回退。
4. 分析师占用键为“窗口编号”（`floor(now/Wn)`）而非时间戳；查询记录永久保存其预留时窗口，结算/撤销只改该旧窗口，旧窗口不过期，因此与历史窗口数无关。
5. 数据集 resv 是标量（不按查询存余额），每次预留 +cost；结算时 −cost/+actual，Cancel Reserved −cost，Cancel Running −cost/+cost。used 单调不减。
## 放弃的方案
6. Running 撤销曾考虑退还未用预留；放弃：带噪结果可能已泄露，必须按全额 cost 计入 used，仅释放 resv，分析师占用保持 cost。
7. 分析师占用曾想在结算时重归当前窗口；放弃：规格要求归属预留窗口，跨窗未结算查询不占新窗口，结算差额只退回旧窗口。
8. 计划求值曾想分“校验 + 求值”两趟；放弃：一趟后序递归同时产出 parts 并集、cost 与所有结构错误，保证 `nodes` 恰为节点总数（错误节点也计入访问）。
9. 曾考虑按查询记录数据集分摊额；放弃：每个数据集承担全额 cost（不除以数据集数），分析师只计一次 cost。
## 拒绝次序
10. Reserve：参数非法（含计划越界/Par 相交以外的结构问题）> 时钟回退 > qid 重复 > 数据集/分析师不存在 > `ErrNotDisjoint` > `ErrDatasetExhausted`（名字节序首个）> `ErrAnalystExhausted`。计划在锁外求值一次；Par 相交错误缓存到存在性检查之后再报。
11. Start/Commit/Cancel：参数非法 > 时钟回退 > qid 不存在 > 状态不符 > `ErrOverspend`。哨兵错误全部 `errors.Is` 可区分，非法参数统一 `ErrInvalidArgument` 包装。
## 复杂度计数器
12. `touched`：每次 Reserve/Commit/Cancel 前置归零，计“访问/写入的账目条数”= 查询记录 1 + 数据集记录 k + 分析师窗口账 1 = k+2，与在途查询数、历史窗口数无关；map 键上不做遍历。
13. `nodes`：计划求值递归中每进入一个节点 +1，恰等于树节点总数，≤256；深度 ≤8 按根深度 1 计。
## 取整与溢出
14. Sample 在该节点处做一次 `ceil(child*num/den)`（用 `(x*num+den-1)/den`，int64 下 x≤1e12、num≤1e6，乘积安全）。只对根 cost 强制 ≤1e12（中间结果仅受 int64 约束）。
## 本地验证
15. `PATH=/usr/local/go/bin:$PATH go build ./...`；`go test ./...`；`go test -race ./...`；`go vet ./...`；`gofmt -l .`。
16. 测试：规则表驱动用例 + 1500 组随机序列，朴素模拟逐查询重算 used/resv/窗口占用并与账本逐项对照，t.Logf 打印每组输入、输出与判定依据；另测 touched=n+2（在途 100/10000 两档）与 nodes 精确计数。
