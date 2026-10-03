# 协议版本与特性协商器设计说明

## 结构
- `caps`：不可变特性表 `Table`（32 项 `(minV,maxV,role)`，位运算判定可用）；`Hello{Lo,Hi,Sup,Req}` 及统一参数校验；错误类型与拒绝原因常量。
- `negotiate`：纯函数 `Pick(table, client, cr, server)`，无锁无 I/O；按 NoVersion→Missing→Denied→Window 次序判定，返回 `Outcome{Ver, Enabled, lower,upper,L,H,Q…}`（诊断字段供日志复现）。
- `session`：单把 `sync.RWMutex` 串行化所有公开操作；会话保存客户端 Hello/cr 快照、版本、E、U；sid 由 1 连续递增。

## 关键取舍
- 版本选择：取窗口内**最高可用版本** `upper`（可立即获得最新特性，upper 受必需特性 `maxV-1` 上界约束）。放弃最低兼容版本：那会平白丧失新特性，且 maxV 上界仍需计算，收益为零。
- 重协商：Q 并入使用中集合 U，**任一判定失败即整体失败并保持原版本/E/U**，成功时 U 必然属于新 E（"使用中特性钉住版本"）。放弃"允许降级并丢弃 U"：正在使用的特性被静默摘除会破坏上层协议，宁可不动。
- 非必需特性权限：仅当 role>cr（或在 v 不可用）时从 E 中**静默排除**，不报错；只有必需特性缺失/越权/窗口冲突才拒绝。放弃整体拒绝：个别高角色特性不应阻断整条链路。
- Missing 先于 Denied 先于 Window：特性是否存在是角色判定的前提，角色又是窗口判定的前提，固定次序保证拒绝原因可精确复现。
- 服务端声明用值拷贝保存，`SetServer` 只影响其后的协商；一把锁保证操作等价于某串行顺序，拒绝路径不做任何写操作。

## 复杂度与复现
- 协商只扫 32 个特性位（常数倍 ≤128 次检视，`Inspected()` 原子计数可断言），与 hi-lo 无关；版本区间仅做 max/min 整数运算。
- 结果完全由特性表、两端 Hello、cr 与操作序列决定；测试以固定种子随机重放，并与逐版本枚举的朴素模拟对拍。

## 本地验证
```bash
gofmt -l . && go vet ./...
go test -race -v ./...
go test -run TestNaive -count=1 ./session   # 2000 组随机对拍
```
