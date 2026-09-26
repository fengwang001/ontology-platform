# NOTES

## 第三节：七步推导

| 步 | 操作 | 返回值 |
|---|---|---|
| S1 | Set("r1",{0:1}) | OK，r1={0:1} |
| S2 | Set("r2",{0:1,1:2}) | OK，r2={0:1,1:2} |
| S3 | Set("r3",{1:1,2:1}) | OK，r3={1:1,2:1} |
| S4 | Merge("r1","r2") | {0:1,1:2} |
| S5 | Merge("r2","r3") | {0:1,1:2,2:1} |
| S6 | Compare("r1","r2") | Less（key0 相等，key1：0<2） |
| S7 | Compare("r1","r3") | Concurrent（key0：1>0，key1：0<1） |

- (甲) S4 正确结果 {0:1,1:2}。若逐 key 取 min：key1 上 min(0,2)=0，错成 {0:1}（即 {0:1,1:0}），actor 1 的计数 2 丢失。
- (乙) S5 正确结果 {0:1,1:2,2:1}。若只遍历 r2 的 key：漏掉只在 r3 出现的 key，错成 {0:1,1:2}，actor 2 的计数 1 被漏掉。
- (丙) S6 正确为 Less。若只比较共有 key（仅 key0，二者相等）：误判为 Equal。S7 正确为 Concurrent（共有 key 为空，该错误实现会误判 Equal）。

## 第二节：四条不变量的保证位置与钉住测试

1. 与朴素重算一致：`vv.Merge` 对两 map 逐条目取 max（vv/vv.go），测试 `TestMergeAgainstNaive`（api/api_test.go，随机向量对拍朴素重算）。
2. 合并是上确界：交换律/幂等/>=两输入由 max 语义保证（vv/vv.go），测试 `TestMergeIsJoin`。
3. 比较三歧正确：`vv.Compare` 枚举两 map key 并集统计 less/greater（vv/vv.go），测试 `TestCompareTrichotomy`（含缺失视为 0 的用例）。
4. 失败不留痕：`reg.Set` 先整体校验再写入、`reg.Merge/Compare` 先查名再算（reg/reg.go），测试 `TestFailureLeavesNoTrace`；三类哨兵错误互不相同由 `TestErrorsDistinct` 钉住。

并发安全：reg 用 `sync.RWMutex`，vv 的读取计数用 `sync/atomic`；测试 `TestConcurrentMergeConsistent`。
