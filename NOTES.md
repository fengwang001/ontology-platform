# NOTES

## 八行分步表（C=4；槽记为 `状态/gen`，O=OPEN，F=FREE）

| # | 操作 | 命中槽与世代 | 槽0 | 槽1 | 槽2 | 槽3 | 结果 |
|---|------|------|------|------|------|------|------|
| 1 | Open | 0→g1 | O/1 | F/0 | F/0 | F/0 | Handle{0,1} |
| 2 | Open | 1→g1 | O/1 | O/1 | F/0 | F/0 | Handle{1,1} |
| 3 | Open | 2→g1 | O/1 | O/1 | O/1 | F/0 | Handle{2,1} |
| 4 | Close({1,1}) | 1 释放，gen 留 1 | O/1 | F/1 | O/1 | F/0 | nil，清空槽1数据 |
| 5 | Open | 最小空闲=1（非3），g1→g2 | O/1 | O/2 | O/1 | F/0 | Handle{1,2} |
| 6 | Recv(1,g1,"old") | 1 是 OPEN 但 g1≠g2 | 不变 | 不变 | 不变 | 不变 | ErrStale |
| 7 | Recv(3,g1,"x") | 3 从未打开，FREE | 不变 | 不变 | 不变 | 不变 | ErrHalfOpen |
| 8 | Recv(1,g2,"hi") | 1 OPEN 且世代匹配 | 不变 | 不变 | 不变 | 不变 | 投递，Data(1)="hi" |

(甲) 不看 gen 时 `"old"` 被投给 connID=1 的**新**连接（gen2），`Data(1)` 成 `"oldhi"`：旧连接迟到帧串进新连接数据流，接收方无法区分，世代隔离失效。
(乙) 静默丢弃：对端以为帧已送达、连接仍存活，继续发往无人接收的槽——丢数据且无报错，半开无法诊断。自动新建：只有 `Open` 能分配槽并递增 gen；自动新建绕过槽分配与世代管理（gen 取值无定义、白占一个槽），违背「FREE 槽必须报 ErrHalfOpen」的判定规则。
(丙) 返回 **3**（next 单调：0,1,2 之后轮到 3）。长期后果：C 个 id 用完后 `Open` 永远 ErrNoSlots，即使槽已释放——connID 空间耗尽，系统一生只能服务 C 条连接。

## 四条不变量的保证位置与钉住测试

1. 朴素一致：demux 先校验再调 slot 原语，规则逐条对应（`demux/demux.go` Open/Close/Recv）；测试 `TestNaiveReference`（随机序列与朴素参照对拍）。
2. 世代隔离：`Recv` 中 gen 不匹配即 ErrStale、不追加（`demux/demux.go` Recv）；测试 `TestGenerationIsolation`。
3. 半开可判定：`validate` 中槽非 OPEN 即 ErrHalfOpen（`demux/demux.go` validate）；测试 `TestHalfOpen`。
4. 失败不留痕：所有校验先于任何状态修改，失败直接返回（`demux/demux.go` validate 先行）；测试 `TestFailureNoSideEffect`。
