# 读己之写会话一致性：推导与不变量

## 七步推导（起点：三副本全空，ver[k]=0，writeVer[k]=0；"-,0" 表示该 key 不存在）

| 步 | 操作 | R0 | R1 | R2 | writeVer[k] | Read 返回（来源） |
|--|--|--|--|--|--|--|
| 1 | Write(A) | (A,1) | -,0 | -,0 | 1 | — |
| 2 | Read | (A,1) | -,0 | -,0 | 1 | (A,1)，来自 R0（R1.ver=0 < wv=1，回退主副本） |
| 3 | Sync(1) | (A,1) | (A,1) | -,0 | 1 | — |
| 4 | Write(B) | (B,2) | (A,1) | -,0 | 2 | — |
| 5 | Sync(2) | (B,2) | (A,1) | (B,2) | 2 | — |
| 6 | Write(C) | (C,3) | (A,1) | (B,2) | 3 | — |
| 7 | Read | (C,3) | (A,1) | (B,2) | 3 | (C,3)，来自 R0（R1.ver=1 < wv=3，回退主副本） |

- (甲) 不检查 writeVer、固定读 R1：第 2 步 R1 上 k 不存在，返回 ("",0)；正确应返回 (A,1)（来自 R0）。
- (乙) writeVer 记成 ver-1：第 7 步 wv=2，R2(ver=2>=2) 被误判为已追平（真实需 ver>=3），从 R2 读到旧值 (B,2)；正确应返回 (C,3)（来自 R0）。（严格按"只查粘滞 R1"的路由，wv=2 时 R1.ver=1<2 仍回 R0，本步恰好不显现；被误判的副本是 R2。）
- (丙) 第 3 步把"R1 已追平"(1>=1) 缓存、之后写新值不再重算：第 7 步直接读 R1，返回 (A,1)；正确应返回 (C,3)（来自 R0）。

## 四条不变量：保证位置 → 钉住它的测试

1. 读己之写：`ses.(*Session).Read` 路由判定（R1.ver>=wv 才读 R1，否则回 R0）→ `api.TestReadYourWrites`
2. 与朴素参照一致：写只落 R0（`kv.(*Primary).Write`），View[0] 恒等于参照、Read 返回值与参照同版本同值 → `api.TestNaiveReference`
3. 版本单调：`kv.(*Primary).Write` 只自增；`ses.(*Cluster).Sync` 整态拷贝不回退 → `ses.TestSyncNoRegression`
4. 失败不留痕：`ses` 中校验（空 key / 越界 idx / 已关闭会话）先于一切状态修改 → `api.TestRejectNoTrace`
