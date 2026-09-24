package main
import("fmt";"math/rand";"ontology/search";"ontology/vec")
func main(){
 const d,n,nq,K=16,5000,100,10
 for _, cfg:=range [][3]float64{{0.3,.25,25},{0.35,.2,40},{0.4,.18,60},{0.45,.18,80}}{
  rng:=rand.New(rand.NewSource(99)); cs,g:=cfg[2],cfg
  centers:=make([][]float64,int(cs))
  for i:=range centers{c:=make([]float64,d);for j:=range c{c[j]=rng.NormFloat64()*g[0]};centers[i]=c}
  pick:=func()vec.Vec{c:=centers[rng.Intn(int(cs))];x:=make(vec.Vec,d);for j:=range x{x[j]=c[j]+rng.NormFloat64()*g[1]};return x}
  vs:=make([]vec.Vec,n);for i:=range vs{vs[i]=pick()}
  qs:=make([]vec.Vec,nq);for i:=range qs{qs[i]=pick()}
  idx,_,_:=search.Build(d,8,8,7,vs)
  var mean,maxc,rec float64
  for _,q:=range qs{ann,nc,_:=idx.Query(q,K);ex,_:=idx.BruteForce(q,K)
   hit:=map[uint32]bool{};for _,r:=range ann{hit[uint32(r.ID)]=true};var inter int;for _,r:=range ex{if hit[uint32(r.ID)]{inter++}}
   mean+=float64(nc);if float64(nc)>maxc{maxc=float64(nc)};rec+=float64(inter)/K}
  fmt.Printf("center=%v noise=%v k=%d -> candMean=%.1f max=%d recall=%.3f\n",g[0],g[1],int(cs),mean/nq,int(maxc),rec/nq)
 }
}
