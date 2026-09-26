# NOTES — ontology-683 稀疏向量点积

推导：m=10；A idx=[1,3,5] val=[2,3,5]；B idx=[0,1,5] val=[4,7,6]。

五行归并（第 5 行为终止行）：

1. i=0 j=0：A1>B0，不匹配，仅 j++，贡献 0，累计 0
2. i=0 j=1：A1=B1，匹配，2·7=14，双 ++，累计 14
3. i=1 j=2：A3<B5，不匹配，仅 i++，贡献 0，累计 14
4. i=2 j=2：A5=B5，匹配，5·6=30，双 ++，累计 44
5. i=3 j=3：i=lenA 且 j=lenB，终止；点积 = 44

(甲) 循环上界写成 i<lenA-1：漏掉下标 5（少 5·6=30）→ 错成 14。
(乙) 忽略 idx 按位置对齐：2·4+3·7+5·6 = 8+21+30 → 错成 59。
(丙) 不等时两指针同时前进：首步 (1,0) 双进跳过 B 的下标 1，只剩 5 匹配 → 错成 30。

不变量（代码保证位置 / 钉住它的测试函数）：

1. 与稠密参照逐位一致：sdot/sdot.go 的 dotAccesses 两指针归并；TestDotMatchesDense（sdot/sdot_test.go）。
2. 规范形：spv/spv.go 的 Set 二分定位后插入/覆盖/删除；api/api.go 的 Build 先整体校验再构造；TestCanonicalForm（api/api_test.go，外部包经公开 API 测）。
3. 只访问非零（访问数恰为 nnzA+nnzB，与 m 无关）：sdot/sdot.go dotAccesses 内的 dotCounter.seen（atomic.Int64），每访问一条加 1、尾部排空；TestAccessCounterIndependentOfM（sdot 同包白盒）。
4. 失败不留痕：spv.go 的 Set 越界检查先于任何写入；api.go 的 Build 四项校验全在 New/Set 之前；TestRejectedOpsNoTrace（api/api_test.go，覆盖 Set 与 Build 两条路径）。

并发只读：Dot/Get 只读、计数器为每调用私有且原子递增；TestConcurrentDotSameResult（sdot/sdot_test.go），go test -race 干净。
