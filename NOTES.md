# NOTES：ontology-323 推导与不变量
规则：h=0；对 UTF-8 字节逐个 h=h*31+b（uint64 回绕）；bucket=h mod 10000；采样 iff bucket<r（严格小于）。
## 第三节：十行分步表（New(2500,100)，八个事件后 SetRate(2000) 再 SetRate(5000)）
|行|键|h(key) 手算算式|h|bucket|当时 r|采样|
|1|gnj|(103·31+110)·31+106=3303·31+106=102393+106|102499|2499|2500|是|
|2|dzv|(100·31+122)·31+118=3222·31+118=99882+118|100000|0|2500|是|
|3|gnk|(103·31+110)·31+107=3303·31+107=102393+107|102500|2500|2500|否（2500≮2500）|
|4|kcm|(107·31+99)·31+109=3416·31+109=105896+109|106005|6005|2500|否|
|5|dzv|同第 2 行|100000|0|2500|是|
|6|qjy|(113·31+106)·31+121=3609·31+121=111879+121|112000|2000|2500|是（2000<2500）|
|7|a6m|(97·31+54)·31+109=3061·31+109=94891+109|95000|5000|2500|否|
|8|dzv|同第 2 行|100000|0|2500|是|
|9|SetRate(2000)|新增=在[2000,2500)桶内的已知键，无|—|—|2500→2000|added=[]；removed=[qjy(b2000),gnj(b2499)]|
|10|SetRate(5000)|新增=桶在[2000,5000)内的已知键，无移出|—|—|2000→5000|added=[qjy(2000),gnj(2499),gnk(2500)]；removed=[]|
## (甲) 八事件保留 5 个：gnj、dzv×3、qjy；dzv 三个事件全部保留（同键全留）。若每事件独立以 25% 随机：P(全留或全丢)=(1/4)^3+(3/4)^3=28/64=7/16=43.75%，即 56.25% 概率裂开，违反不变量 2（同键全留全丢/跨实例一致）。
## (乙) 错误实现 h mod M==0：r=2500(M=4)：{dzv,gnk,qjy,a6m}（100000/102500/112000/95000 整除 4；102499≡3、106005≡1 否）；r=2000(M=5)：{dzv,gnk,kcm,qjy,a6m}（106005 整除 5；102499≡4 否）；r=5000(M=2)：{dzv,gnk,qjy,a6m}（奇 h 的 gnj、kcm 否）。调低到 2000 反被纳入、调高到 5000 反被移出的都是 kcm，违反不变量 3（单调性）。
## (丙) 边界桶：gnk=2500（首访 2500 不采）、qjy=2000（SetRate(2000) 时 2000≮2000，不保留而被移出；到 5000 才纳入）、a6m=5000（SetRate(5000) 时 5000≮5000，不纳入）。若改 bucket<=r：Feed 保留 6 个（多 gnk）；SetRate(2000)→removed=[gnj,gnk]、added=[]；SetRate(5000)→added=[gnj(2499),gnk(2500),a6m(5000)]、removed=[]。
## 第二节四条不变量：代码位置 / 钉住的测试函数
1. 朴素参照一致：判定纯函数 khash.Sampled（khash/khash.go）；Feed 逐事件直判（api/api.go Feed）；SetRate 只取区间[min,max)内有序键（samp/samp.go SetRate）→ TestFeedMatchesNaive、TestSetRateMatchesNaive。
2. 同键全留全丢/跨实例一致：khash 无状态、Sampled 只由 (key,r) 决定（khash/khash.go）→ TestCrossInstanceConsistency、TestConcurrentFeeds。
3. 单调：bucket<r 是 r 的单调谓词，SetRate 差集恰为桶区间[min,max)，调高必无 removed、调低必无 added（samp/samp.go）→ TestMonotonicRates。
4. 失败不留痕：New 校验后才构造；Feed/SetRate 先整批校验（空键/超限/越界）通过后才改状态（api/api.go）→ TestRejectedOpsNoTrace。
