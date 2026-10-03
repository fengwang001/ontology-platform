# 设计说明：带复用策略矩阵的工作流实例登记表

## 模块划分
- `policy`：纯判定。输入 reuse、conflict 与现有记录状态，输出 `StartVerdict`，无状态、无锁。
- `acl`：主体 -> admin 标志，独立 mutex；`CanTerminate(p, owner, run)` 仅在调用点读快照，避免与 registry 锁形成嵌套。
- `registry`：单把 mutex 串行化 Start/Finish；map[id]*rec（存活记录，至多一条）+ 全局 run 号 + 单调时钟 + 过期最小堆。

## 关键取舍
- **Terminate 路径绕过 reuse**：调用方已显式要求“终止并取代”运行中实例，语义是取代而非复用。放弃方案“先终止再按 reuse 判定”——那会让 `Reject` 永远无法取代一个 Running（死局），且终止副作用已发生却返回拒绝。
- **UseExisting 不校验权限**：该路径幂等、不创建、不改记录、不终止，仅返回既有 run 号。放弃方案“复用也校验所有者”——会让非所有者的幂等重试（如服务重启后重复启动）无意义地失败。
- **过期即视同不存在，不保留墓碑**：保留期是确定性纯时间函数（now ≥ end+R），墓碑不承载任何判定信息，只浪费容量与内存；过期项由最小堆惰性清除，替换产生的旧堆项作废并由版本号识别。
- **替换不查容量**：Running 被 Terminate 替换、已结束记录被复用替换，ID 数均不增加；只有“无存活记录”的新建检查容量 N。

## 判定顺序与错误优先级
1. 参数非法（空 id/主体、枚举与构造参数越界、now 越界）；2. `ErrClock`（now 小于全局时钟）；
3. Start：`ErrRunning` → `ErrDenied` → `ErrReuse` → `ErrCapacity`；Finish：`ErrNotFound` → `ErrStale` → `ErrNotRunning`。
拒绝不改任何状态（run 号、时钟、记录、堆）。UseExisting 视为成功：推进时钟但不耗 run 号。

## 过期堆与复杂度
堆项 =（过期时刻 end+R, id, 版本号）。惰性清除只在堆顶到期时弹出；一次操作考察项数 ≤ 到期项数 + 1
（作废项若恰好位于堆顶到期序列中也被顺带弹出，仍受该界约束）。非导出计数器 `peepCount` 供测试验证两档规模。

## 本地验证
```bash
go build ./...
go test -race -v ./...
go test -run TestRandom -count=10 ./registry
gofmt -l . && go vet ./...
```
