# 时限授权 / 转授 / 自动回收 设计说明

## 分层
- `grant`：无锁数据结构。`Node{ID,User,Res,Start,End,Depth,ParentID,Exts,alive}` + `Tree`（父→子有序、`(u,r)` 活动索引、主体计数）。
- `request`：无锁申请簿。`Request` 状态 Pending/Approved/Denied，按提交时刻 +P 判过期（恰等过期）。
- `reaper`：无锁回收器。`Log(grantID,cause,at)`，按 `(end,id)` 升序收到期授权，并对子树先序（子按 id 升序、孙紧随其子）记 Cascaded；冷却表只登记直接 Revoked 的 `(u,r)`。
- 根包 `access`：单 `sync.RWMutex` 串行化全部变更，等价某一串行顺序；所有校验先于落地，拒绝不占号、不落地、不推进时钟。

## 关键取舍
- 延长自**原 end**累加（`end += extra`），不用 `max(end,now)+extra`：now 晚于 end 的根已失效、now ≤ end 时两式相同，故仅原式语义自洽。
- 父延长**不带动子**：子 end 在创建时被父 end 封顶即固化；放弃“子随父顺延”，否则封顶约束会被事后突破，且无法区分 Cascaded/Expired。
- 封顶子到自己 end 时父仍活 → 记 Expired；父先到 end → 子记 Cascaded。判定依据“处理主序列时该节点是否仍活”。
- Check 沿 ParentID 上溯最多 3 个 Node（自身 + 两级祖先），`touched` 精确计数，与全局规模无关；只读、不回收、不推进时钟。
- 活动索引 `(u,r)→grantID` 与计数由 Tree 在创建/失效时同步维护，使 ErrActiveGrant/ErrLimit 为 O(1)。
- 每个被接受的变更（含 SetOwners/Tick）先统一 sweep：排序到期节点→逐个 Expired 后立即先序级联；重复节点天然跳过（alive=false 不再进主序列）。
- 冷却仅直接 Revoked 登记，级联与自然到期不登记；同 `(u,r)` 再次直接撤销则刷新截止时刻。
- now 为 int64 秒，单调时钟（<maxNow 即 ErrClock，恰等允许）；区间半开 `[start,end)`，恰等到期/恰等冷却解除。

## 被放弃的方案
- 每授权独立 goroutine + timer 回收：无法保证确定性的 `(end,id)` 日志次序与重放一致。
- Check 顺带落地回收：违反“只读、不推进时钟”，且会改变拒绝操作的观测结果。

## 验证
- `go build ./...`、`go vet ./...`、`gofmt -l .`、`go test -race ./...`。
- 表驱动覆盖各边界（恰等过期/冷却、总时长恰 Lmax、封顶子 Cascaded、父延长后子 Expired、孙紧随其子、不占号、拒绝次序）。
- 独立朴素模拟器（全量扫描重建状态）与 1500 组随机操作序列对照 Reaped、错误、Check；另测 100/10000 授权下 touched≤3。
