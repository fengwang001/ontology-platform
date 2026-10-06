"""Independent naive reference model.

This implementation deliberately uses plain lists and full rescans: it is
written straight from the natural-language specification and shares no
internal machinery with :mod:`mptc.scheduler`. Its purpose is differential
testing of the efficient implementation.
"""

from __future__ import annotations

from .errors import ErrCode, SchedulerError
from .types import Result, SentSegment, SubflowRole


class NaiveScheduler:
    def __init__(self, mss):
        if not isinstance(mss, int) or isinstance(mss, bool) or mss <= 0:
            raise SchedulerError(ErrCode.INVALID_PARAM, "mss")
        self.mss = mss
        self.flows = []  # dicts
        self.segments = {}  # seq -> length, append-only log
        self.new_queue = []  # [seq]
        self.reinject_queue = []  # [seq]
        self.next_seq = 0
        self.sent_end = 0
        self.acked = 0
        self.window = 0
        self.has_window = False

    # ------------------------------------------------------------- helpers

    def _find(self, sid):
        for flow in self.flows:
            if flow["sid"] == sid:
                return flow
        return None

    def _send(self):
        out = []
        while True:
            # Drop reinjected copies covered by the cumulative connection ack.
            self.reinject_queue = [
                seq
                for seq in self.reinject_queue
                if seq + self.segments[seq] > self.acked
            ]

            normal_exists = any(
                f["active"] and f["role"] is SubflowRole.NORMAL for f in self.flows
            )

            if self.reinject_queue:
                seq = min(self.reinject_queue)
                kind = "reinject"
            elif self.new_queue:
                seq = self.new_queue[0]
                if self.has_window and seq + self.segments[seq] > self.acked + self.window:
                    break
                kind = "new"
            else:
                break
            length = self.segments[seq]
            candidates = []
            for flow in self.flows:
                if not flow["active"]:
                    continue
                if normal_exists and flow["role"] is SubflowRole.BACKUP:
                    continue
                used = sum(length2 for _seq2, length2 in flow["inflight"])
                if flow["cwnd"] - used < length:
                    continue
                candidates.append(flow)
            if not candidates:
                break
            candidates.sort(key=lambda f: (f["rtt_ms"], f["sid"]))
            chosen = candidates[0]
            chosen["inflight"].append((seq, length))
            if kind == "reinject":
                self.reinject_queue.remove(seq)
            else:
                self.new_queue.pop(0)
            if seq + length > self.sent_end:
                self.sent_end = seq + length
            out.append(SentSegment(seq, length, chosen["sid"], kind == "reinject"))
        return Result(tuple(out))

    # ----------------------------------------------------------------- API

    def add_subflow(self, sid, rtt_ms, cwnd, role=SubflowRole.NORMAL):
        if not isinstance(sid, str) or sid == "":
            raise SchedulerError(ErrCode.INVALID_PARAM, "sid")
        if self._find(sid) is not None:
            raise SchedulerError(ErrCode.INVALID_PARAM, "dup")
        if not isinstance(rtt_ms, int) or isinstance(rtt_ms, bool) or rtt_ms <= 0:
            raise SchedulerError(ErrCode.INVALID_PARAM, "rtt")
        if (
            not isinstance(cwnd, int)
            or isinstance(cwnd, bool)
            or cwnd < self.mss
        ):
            raise SchedulerError(ErrCode.INVALID_PARAM, "cwnd")
        if not isinstance(role, SubflowRole):
            raise SchedulerError(ErrCode.INVALID_PARAM, "role")
        self.flows.append(
            {
                "sid": sid,
                "init_rtt": rtt_ms,
                "init_cwnd": cwnd,
                "rtt_ms": rtt_ms,
                "cwnd": cwnd,
                "role": role,
                "active": True,
                "inflight": [],
            }
        )
        return self._send()

    def write(self, data):
        if not isinstance(data, (bytes, bytearray)) or len(data) == 0:
            raise SchedulerError(ErrCode.INVALID_PARAM, "data")
        remaining = len(data)
        while remaining:
            piece = min(self.mss, remaining)
            self.segments[self.next_seq] = piece
            self.new_queue.append(self.next_seq)
            self.next_seq += piece
            remaining -= piece
        return self._send()

    def subflow_ack(self, sid, bytes_acked):
        if not isinstance(sid, str) or sid == "":
            raise SchedulerError(ErrCode.INVALID_PARAM, "sid")
        if (
            not isinstance(bytes_acked, int)
            or isinstance(bytes_acked, bool)
            or bytes_acked < 0
        ):
            raise SchedulerError(ErrCode.INVALID_PARAM, "ack")
        flow = self._find(sid)
        if flow is None:
            raise SchedulerError(ErrCode.SUBFLOW_NOT_FOUND, sid)
        if not flow["active"]:
            return Result(())
        total = sum(length for _seq, length in flow["inflight"])
        if bytes_acked == 0:
            return self._send()
        if bytes_acked > total:
            raise SchedulerError(ErrCode.ACK_OUT_OF_RANGE)
        prefix = 0
        boundary = False
        cut = 0
        for index, (_seq, length) in enumerate(flow["inflight"]):
            prefix += length
            if prefix == bytes_acked:
                boundary = True
                cut = index + 1
                break
            if prefix > bytes_acked:
                break
        if not boundary:
            raise SchedulerError(ErrCode.ACK_NOT_SEGMENT_ALIGNED)
        flow["inflight"] = flow["inflight"][cut:]
        return self._send()

    def connection_ack(self, ack_seq, window):
        if (
            not isinstance(ack_seq, int)
            or isinstance(ack_seq, bool)
            or ack_seq < 0
            or not isinstance(window, int)
            or isinstance(window, bool)
            or window < 0
        ):
            raise SchedulerError(ErrCode.INVALID_PARAM, "ack")
        if ack_seq < self.acked:
            raise SchedulerError(ErrCode.STALE_ACK)
        if ack_seq > self.sent_end:
            raise SchedulerError(ErrCode.INVALID_PARAM, "ack>sent")
        self.window = window
        self.has_window = True
        if ack_seq > self.acked:
            self.acked = ack_seq
        return self._send()

    def subflow_fail(self, sid):
        if not isinstance(sid, str) or sid == "":
            raise SchedulerError(ErrCode.INVALID_PARAM, "sid")
        flow = self._find(sid)
        if flow is None:
            raise SchedulerError(ErrCode.SUBFLOW_NOT_FOUND, sid)
        if flow["active"]:
            moved = [
                seq
                for seq, length in flow["inflight"]
                if seq + length > self.acked
            ]
            moved.sort()
            for seq in moved:
                if seq not in self.reinject_queue:
                    self.reinject_queue.append(seq)
            flow["inflight"] = []
            flow["active"] = False
        return self._send()

    def subflow_recover(self, sid):
        if not isinstance(sid, str) or sid == "":
            raise SchedulerError(ErrCode.INVALID_PARAM, "sid")
        flow = self._find(sid)
        if flow is None:
            raise SchedulerError(ErrCode.SUBFLOW_NOT_FOUND, sid)
        if not flow["active"]:
            flow["active"] = True
            flow["rtt_ms"] = flow["init_rtt"]
            flow["cwnd"] = flow["init_cwnd"]
            flow["inflight"] = []
        return self._send()

    def change_rtt(self, sid, rtt_ms):
        if not isinstance(sid, str) or sid == "":
            raise SchedulerError(ErrCode.INVALID_PARAM, "sid")
        if not isinstance(rtt_ms, int) or isinstance(rtt_ms, bool) or rtt_ms <= 0:
            raise SchedulerError(ErrCode.INVALID_PARAM, "rtt")
        flow = self._find(sid)
        if flow is None:
            raise SchedulerError(ErrCode.SUBFLOW_NOT_FOUND, sid)
        flow["rtt_ms"] = rtt_ms
        return self._send()

    def snapshot(self):
        return {
            "mss": self.mss,
            "next_seq": self.next_seq,
            "sent_max_end": self.sent_end,
            "acked_seq": self.acked,
            "window": self.window,
            "have_window_ad": self.has_window,
            "unsent": [(seq, self.segments[seq]) for seq in self.new_queue],
            "reinject": sorted((seq, self.segments[seq]) for seq in self.reinject_queue),
            "flows": {
                flow["sid"]: {
                    "rtt_ms": flow["rtt_ms"],
                    "cwnd": flow["cwnd"],
                    "role": flow["role"].value,
                    "active": flow["active"],
                    "inflight_bytes": sum(length for _seq, length in flow["inflight"]),
                    "inflight": list(flow["inflight"]),
                }
                for flow in sorted(self.flows, key=lambda f: f["sid"])
            },
        }
