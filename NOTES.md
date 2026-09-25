# NOTES

八步推演（`New("sekret")`；A、B 配额均为 maxKeys=2, maxBytes=100；状态记 A=(keyCount,totalBytes){keys}）：

| # | 操作 | A | B | 结果 |
|---|---|---|---|---|
| 1 | Register(A,2,100) | (0,0){} | — | 成功 |
| 2 | Register(B,2,100) | (0,0){} | (0,0){} | 成功 |
| 3 | Put(A,x,"1") | (1,1){x} | (0,0){} | 成功 |
| 4 | Put(A,y,"22") | (2,3){x,y} | (0,0){} | 成功 |
| 5 | Put(A,z,"333") | (2,3){x,y} | (0,0){} | 拒：新 key 且 keyCount 2≥2（ErrQuotaKeys） |
| 6 | Put(A,x,"4444") | (2,6){x,y} | (0,0){} | 成功：更新不增 key，3−1+4=6 |
| 7 | Put(B,x,"B") | (2,6){x,y} | (1,1){x} | 成功 |
| 8 | Get(A,x)/Get(B,x) | "4444" | "B" | 成功，两值不同 |

- （甲）扁平全局表：第 7 步把全局 x 覆盖成 "B"，第 8 步 Get(A,x) 错得 **"B"**；正确是 **"4444"**。
- （乙）更新误算新增：第 6 步以 keyCount=2≥maxKeys=2 被**错拒 ErrQuotaKeys**，x 停留 "1"、A=(2,3)；正确应成功，x="4444"、A=(2,6)。
- （丙）Purge 不校验 token：普通调用者把 B 清空（(1,1)→(0,0)，x 丢失）；正确返回 **ErrUnauthorized**，B 状态不变。

## 不变量保证位置与钉住测试

1. 租户隔离：`store` 用 `map[id]*ten.Tenant` 按 id 路由（store.go），同名 key 落在不同 map —— `TestIsolationAndNaiveReference`。
2. 朴素参照一致：`ten.Put/Get/Del` 即其私有 map 上的朴素增查删（ten.go）—— `TestIsolationAndNaiveReference`。
3. 配额准确：`ten.Put/Del` 只做增量计数、配额判定先于写入（ten.go）—— `TestUsageMatchesNaiveRecount`。
4. 失败不留痕：store 先验租户、ten 先判空 key/配额后赋值，拒绝路径无写操作 —— `TestRejectionsLeaveNoTrace`。

复杂度：计数器 `ten.lastCheckedKeys` 非导出、不进公开接口；`TestQuotaCheckCountConstant`（白盒）断言 m=100…10000 时覆盖已有 key 后恒为 1。
