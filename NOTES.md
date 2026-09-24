# 两级 LWW-Map 推导与不变量

## 八步分步表（外层键 "o"；A、B 独立演进，第 6 步合并）

| 步 | 墓碑ts | inner 内容 (ts,rep,val) | 可见条目（视图） |
|---|---|---|---|
| 1 A Put k1 | 0 | k1:(1,A,100) | k1=100 |
| 2 A Put k2 | 0 | k1:(1,A,100),k2:(2,A,200) | k1=100,k2=200 |
| 3 A DelOuter | 3 | 同步2（不物理清空） | 无，o 不出现在视图 |
| 4 B Put k3 | 0(B) | k3:(1,B,300) | k3=300 |
| 5 B Put k4 | 0(B) | k3:(1,B,300),k4:(2,B,400) | k3=300,k4=400 |
| 6 Merge(A,B) | 3 | k1..k4 并集（ts 1/2/1/2） | 无（全部 ts<=3），o 消失 |
| 7 C Put k5,ts=3 | 3 | 同步6：迟到写被整体忽略，k5 不写入 | 无 |
| 8 D Put k6,ts=4 | 3 | 步6 并集 + k6:(4,D,600)（无 k5） | k6=600 |

(甲) 合并后墓碑=max(3,0)=3，四 inner 条目 ts 全 <=3，"o" 消失。若合并只并 inner、漏传播墓碑（当 0），"o" 会错复活：k1=100、k2=200、k3=300、k4=400 全部可见。
(乙) k=100（ts=5 胜 ts=2；只有 ts 平局才比 rep）。若按副本名/到达顺序取胜，会错取 999（B 字典序比 A 大、或 B 后到）。
(丙) ts=3 恰好等于墓碑 => 被覆盖，迟到写整体忽略，"o" 仍不出现；ts=4>3 => 可见，视图 {o:{k6:600}}。若错判成 ts>=墓碑，步7 后 "o" 会错复活成 {k5:500}（正确应为 o 仍缺席）。

## 四条不变量：代码保证位置 / 钉住测试

1. 与批量重算一致：lww.go `Merge`（墓碑取 max、inner 并集逐键 LWW）+ `View`（仅 ts>墓碑 可见，空外层键剔除）；api.go `batch` 独立重算 — 钉住：`TestBatchRecompute`。
2. 墓碑传播：lww.go `Merge` 第一个循环对墓碑取两侧 max — 钉住：`TestTombstonePropagation`（双向合并均断言）。
3. 键级 LWW 收敛：lww.go `Put`/`Merge` 的 winner（ts 大者、平局 rep 字典序大者），纯比较无顺序依赖 — 钉住：`TestLWWConvergence`。
4. 失败不留痕：lww.go `Put`/`DelOuter` 在任何 map 写入之前先校验，失败直接返回哨兵错误 — 钉住：`TestRejectedOpsNoTrace`。
