// 远程控车指令网关的 HTTP 演示服务。
//
// 接口（JSON，枚举均用字符串）：
//
//	POST   /vehicles/{vid}/reports              车端状态上报
//	POST   /vehicles/{vid}/commands             提交控制指令
//	POST   /vehicles/{vid}/commands/{id}/acks   指令回执
//	GET    /vehicles/{vid}/commands/{id}        查询指令
//	GET    /vehicles/{vid}                      车辆诊断快照
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"ontology/internal/gateway"
)

var (
	addr        = flag.String("addr", ":8080", "监听地址")
	minBattery  = flag.Int("min-battery", 20, "电量下限（百分比）")
	staleness   = flag.Int64("staleness", 300, "状态陈旧阈值（秒）")
	wakeTimeout = flag.Int64("wakeup-timeout", 60, "唤醒超时（秒）")
	wakeQuota   = flag.Int("wakeup-quota", 10, "每车每自然日唤醒配额")
	dayOffset   = flag.Int64("day-offset", 8*3600, "自然日界秒级时区偏移")
	idemRetain  = flag.Int64("idem-retention", 24*3600, "幂等记录保留时长（秒）")
)

var gearNames = map[string]gateway.Gear{
	"park": gateway.GearPark, "reverse": gateway.GearReverse,
	"neutral": gateway.GearNeutral, "drive": gateway.GearDrive,
}

var powerNames = map[string]gateway.PowerState{
	"sleep": gateway.PowerSleep, "awake": gateway.PowerAwake, "driving": gateway.PowerDriving,
}

var lockNames = map[string]gateway.DoorLock{
	"all_locked": gateway.LockAllLocked, "not_all_locked": gateway.LockNotAllLocked,
}

var cmdNames = map[string]gateway.CmdType{
	"unlock": gateway.CmdUnlock, "lock": gateway.CmdLock,
	"ac_on": gateway.CmdACOn, "ac_off": gateway.CmdACOff,
	"find_car": gateway.CmdFindCar, "open_trunk": gateway.CmdOpenTrunk,
	"remote_start": gateway.CmdRemoteStart,
}

type reportReq struct {
	Seq        int64  `json:"seq"`
	Time       int64  `json:"time"`
	Gear       string `json:"gear"`
	SpeedKmh   int    `json:"speed_kmh"`
	Power      string `json:"power"`
	Lock       string `json:"lock"`
	BatteryPct int    `json:"battery_pct"`
}

type commandReq struct {
	Submitter   string `json:"submitter"`
	RequestID   string `json:"request_id"`
	Type        string `json:"type"`
	Time        int64  `json:"time"`
	ValiditySec int64  `json:"validity_sec"`
}

type ackReq struct {
	Time    int64 `json:"time"`
	Success bool  `json:"success"`
}

type server struct {
	gw *gateway.Gateway
}

func main() {
	flag.Parse()
	gw, err := gateway.NewGateway(gateway.Config{
		MinBatteryPct:           *minBattery,
		StalenessThresholdSec:   *staleness,
		WakeupTimeoutSec:        *wakeTimeout,
		WakeupQuotaPerDay:       *wakeQuota,
		DayOffsetSec:            *dayOffset,
		IdempotencyRetentionSec: *idemRetain,
	})
	if err != nil {
		log.Fatalf("配置非法: %v", err)
	}
	s := &server{gw: gw}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /vehicles/{vid}/reports", s.postReport)
	mux.HandleFunc("POST /vehicles/{vid}/commands", s.postCommand)
	mux.HandleFunc("POST /vehicles/{vid}/commands/{id}/acks", s.postAck)
	mux.HandleFunc("GET /vehicles/{vid}/commands/{id}", s.getCommand)
	mux.HandleFunc("GET /vehicles/{vid}", s.getVehicle)
	log.Printf("控车指令网关 listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var re *gateway.RejectError
	if errors.As(err, &re) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error":  "rejected",
			"reason": re.Reason.String(),
			"detail": re.Detail,
		})
		return
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

func (s *server) postReport(w http.ResponseWriter, r *http.Request) {
	vid := r.PathValue("vid")
	var req reportReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	rep := gateway.StateReport{
		Seq: req.Seq, Time: req.Time,
		Gear: gearNames[req.Gear], SpeedKmh: req.SpeedKmh,
		Power: powerNames[req.Power], Lock: lockNames[req.Lock],
		BatteryPct: req.BatteryPct,
	}
	if _, ok := gearNames[req.Gear]; !ok && req.Gear != "" {
		writeErr(w, fmt.Errorf("未知档位 %q", req.Gear))
		return
	}
	out, err := s.gw.ReportState(vid, rep)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"outcome": out.String()})
}

func (s *server) postCommand(w http.ResponseWriter, r *http.Request) {
	vid := r.PathValue("vid")
	var req commandReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	typ, ok := cmdNames[req.Type]
	if !ok {
		writeErr(w, fmt.Errorf("未知指令类型 %q", req.Type))
		return
	}
	res, err := s.gw.SubmitCommand(gateway.SubmitRequest{
		VehicleID: vid, Submitter: req.Submitter, RequestID: req.RequestID,
		Type: typ, Time: req.Time, ValiditySec: req.ValiditySec,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"command_id": res.CommandID,
		"status":     res.Status.String(),
		"duplicate":  res.Duplicate,
	})
}

func (s *server) postAck(w http.ResponseWriter, r *http.Request) {
	var req ackReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	res, err := s.gw.Ack(r.PathValue("vid"), r.PathValue("id"), req.Time, req.Success)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"late": res.Late})
}

func (s *server) getCommand(w http.ResponseWriter, r *http.Request) {
	view, ok := s.gw.QueryCommand(r.PathValue("vid"), r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "command not found"})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) getVehicle(w http.ResponseWriter, r *http.Request) {
	vid := r.PathValue("vid")
	if strings.HasSuffix(vid, "/") {
		vid = strings.TrimSuffix(vid, "/")
	}
	snap, ok := s.gw.VehicleSnapshot(vid)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "vehicle not found"})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}
