"""Sender-side scheduler for one multipath connection.

Purely event driven: every mutating event returns the segments emitted by the
send decision performed immediately after the event. There is no clock and no
background activity.
"""

from __future__ import annotations

import heapq
import threading
from collections import deque

from .errors import ErrCode, SchedulerError
from .types import Result, SentSegment, SubflowRole


class Scheduler:
    """Connection scheduler.

    Complexity notes (see docs/design.md for the full argument):
    - Event handling touches only segments actually created, moved or released
      by the event; it never scans the unsent queue or event history.
    - Reinjection push/pop is O(log R) where R is the number of segments
      currently awaiting reinjection, plus O(k) for the k segments the event
      moves/releases (amortised O(1) each).
    - A single send decision examines each active subflow once per emitted
      segment; subflow count is connection configuration, independent of queue
      length and history.
    """

    def __init__(self, mss: int):
        if not _is_pos_int(mss):
            raise SchedulerError(ErrCode.INVALID_PARAM, "mss must be a positive int")
        self._mss = mss
        self._flows: dict[str, "_Flow"] = {}
        # Segment table: connection seq -> length. Retained for boundary math.
        self._seg_len: dict[int, int] = {}
        # New data never sent: (seq, length) in arrival order.
        self._unsent: deque[tuple[int, int]] = deque()
        # Connection seq of the next newly produced segment.
        self._next_seq = 0
        # Highest "end" ever sent (max sent seq + sent length); monotonic.
        self._sent_max_end = 0
        # Cumulative connection ack boundary (bytes from seq 0 fully acked).
        self._acked_seq = 0
        # Latest advertised connection receive window (bytes after acked_seq).
        self._window = 0
        self._have_window_ad = False
        # Reinjection min-heap of (seq, length); seq is unique while queued.
        self._reinject: list[tuple[int, int]] = []
        self._reinject_set: set[int] = set()
        self._lock = threading.Lock()
        # Instrumentation: state units touched across the instance.
        self.touch_count = 0

    # ------------------------------------------------------------------ API

    def add_subflow(self, sid, rtt_ms, cwnd, role=SubflowRole.NORMAL):
        with self._lock:
            _require_nonempty_id(sid)
            if sid in self._flows:
                raise SchedulerError(ErrCode.INVALID_PARAM, "duplicate subflow id")
            if not _is_pos_int(rtt_ms):
                raise SchedulerError(ErrCode.INVALID_PARAM, "rtt_ms must be a positive int")
            if not _is_int(cwnd) or cwnd < self._mss:
                raise SchedulerError(
                    ErrCode.INVALID_PARAM, "cwnd must be an int >= mss"
                )
            if not isinstance(role, SubflowRole):
                raise SchedulerError(ErrCode.INVALID_PARAM, "bad role")
            self._flows[sid] = _Flow(sid, rtt_ms, cwnd, role)
            return self._send_locked()

    def write(self, data):
        with self._lock:
            if not isinstance(data, (bytes, bytearray)) or len(data) == 0:
                raise SchedulerError(
                    ErrCode.INVALID_PARAM, "data must be non-empty bytes"
                )
            remaining = len(data)
            while remaining > 0:
                piece = min(self._mss, remaining)
                seq = self._next_seq
                self._seg_len[seq] = piece
                self._unsent.append((seq, piece))
                self.touch_count += 1
                self._next_seq += piece
                remaining -= piece
            return self._send_locked()

    def subflow_ack(self, sid, bytes_acked):
        with self._lock:
            if not _is_nonempty_id_value(sid) or not _is_int(bytes_acked) or bytes_acked < 0:
                raise SchedulerError(ErrCode.INVALID_PARAM, "bad ack arguments")
            flow = self._flows.get(sid)
            if flow is None:
                raise SchedulerError(ErrCode.SUBFLOW_NOT_FOUND, str(sid))
            # Late acks for a failed subflow are ignored silently.
            if not flow.active:
                return Result(())
            if bytes_acked == 0:
                return self._send_locked()
            # A subflow ack counts bytes of the current in-flight stream.
            # Reinjection can make connection seqs non-contiguous here, so the
            # boundary is computed purely from in-flight segment prefix sums.
            if bytes_acked > flow.inflight_bytes:
                raise SchedulerError(ErrCode.ACK_OUT_OF_RANGE)
            running = 0
            cut = -1
            for index, (_seq, length) in enumerate(flow.inflight):
                running += length
                if running == bytes_acked:
                    cut = index + 1
                    break
                if running > bytes_acked:
                    break
            if cut < 0:
                raise SchedulerError(ErrCode.ACK_NOT_SEGMENT_ALIGNED)
            self._release_prefix_locked(flow, cut, bytes_acked)
            return self._send_locked()

    def connection_ack(self, ack_seq, window):
        with self._lock:
            if not _is_int(ack_seq) or ack_seq < 0 or not _is_int(window) or window < 0:
                raise SchedulerError(ErrCode.INVALID_PARAM, "bad ack arguments")
            if ack_seq < self._acked_seq:
                raise SchedulerError(ErrCode.STALE_ACK)
            if ack_seq > self._sent_max_end:
                raise SchedulerError(
                    ErrCode.INVALID_PARAM, "ack beyond sent range"
                )
            self._window = window
            self._have_window_ad = True
            if ack_seq > self._acked_seq:
                self._acked_seq = ack_seq
                self._drop_covered_reinjects_locked()
            return self._send_locked()

    def subflow_fail(self, sid):
        with self._lock:
            if not _is_nonempty_id_value(sid):
                raise SchedulerError(ErrCode.INVALID_PARAM, "bad subflow id")
            flow = self._flows.get(sid)
            if flow is None:
                raise SchedulerError(ErrCode.SUBFLOW_NOT_FOUND, str(sid))
            if flow.active:
                moved = []
                for seq, length in flow.inflight:
                    self.touch_count += 1
                    # A segment is covered only when its whole range lies
                    # inside the cumulative connection ack.
                    if seq + length > self._acked_seq:
                        moved.append((seq, length))
                # Reinjection queue is connection-seq ascending.
                moved.sort(key=lambda item: item[0])
                for seq, length in moved:
                    if seq not in self._reinject_set:
                        heapq.heappush(self._reinject, (seq, length))
                        self._reinject_set.add(seq)
                flow.active = False
                flow.inflight.clear()
                flow.inflight_bytes = 0
            return self._send_locked()

    def subflow_recover(self, sid):
        with self._lock:
            if not _is_nonempty_id_value(sid):
                raise SchedulerError(ErrCode.INVALID_PARAM, "bad subflow id")
            flow = self._flows.get(sid)
            if flow is None:
                raise SchedulerError(ErrCode.SUBFLOW_NOT_FOUND, str(sid))
            if not flow.active:
                flow.active = True
                flow.rtt_ms = flow.init_rtt
                flow.cwnd = flow.init_cwnd
                flow.inflight.clear()
                flow.inflight_bytes = 0
            return self._send_locked()

    def change_rtt(self, sid, rtt_ms):
        with self._lock:
            if not _is_nonempty_id_value(sid) or not _is_pos_int(rtt_ms):
                raise SchedulerError(ErrCode.INVALID_PARAM, "bad rtt arguments")
            flow = self._flows.get(sid)
            if flow is None:
                raise SchedulerError(ErrCode.SUBFLOW_NOT_FOUND, str(sid))
            flow.rtt_ms = rtt_ms
            return self._send_locked()

    # -------------------------------------------------------------- internal

    def _send_locked(self):
        emitted: list[SentSegment] = []
        # Discard reinjected copies already covered by the connection ack.
        self._drop_covered_reinjects_locked()

        any_normal_active = any(
            f.active and f.role is SubflowRole.NORMAL for f in self._flows.values()
        )

        while True:
            if self._reinject:
                seq, length = self._reinject[0]
                reinjected = True
            elif self._unsent:
                seq, length = self._unsent[0]
                # Connection window constrains new sends only.
                if self._have_window_ad and seq + length > self._acked_seq + self._window:
                    break
                reinjected = False
            else:
                break

            chosen = None
            chosen_key = None
            for flow in self._flows.values():
                if not flow.active or flow.inflight_bytes + length > flow.cwnd:
                    continue
                if any_normal_active and flow.role is SubflowRole.BACKUP:
                    continue
                key = (flow.rtt_ms, flow.sid)
                if chosen_key is None or key < chosen_key:
                    chosen_key = key
                    chosen = flow

            if chosen is None:
                # Head of line blocking: do not skip the waiting segment.
                break

            if reinjected:
                heapq.heappop(self._reinject)
                self._reinject_set.discard(seq)
            else:
                self._unsent.popleft()
            chosen.inflight.append((seq, length))
            chosen.inflight_bytes += length
            if seq + length > self._sent_max_end:
                self._sent_max_end = seq + length
            self.touch_count += 1
            emitted.append(SentSegment(seq, length, chosen.sid, reinjected))
        return Result(tuple(emitted))

    def _drop_covered_reinjects_locked(self):
        while self._reinject:
            seq, length = self._reinject[0]
            if seq + length > self._acked_seq:
                break
            seq, _length = heapq.heappop(self._reinject)
            self._reinject_set.discard(seq)
            self.touch_count += 1

    def _release_prefix_locked(self, flow: "_Flow", cut, released_bytes):
        for _ in range(cut):
            flow.inflight.popleft()
            self.touch_count += 1
        flow.inflight_bytes -= released_bytes

    def snapshot(self):
        with self._lock:
            return {
                "mss": self._mss,
                "next_seq": self._next_seq,
                "sent_max_end": self._sent_max_end,
                "acked_seq": self._acked_seq,
                "window": self._window,
                "have_window_ad": self._have_window_ad,
                "unsent": list(self._unsent),
                "reinject": sorted(self._reinject),
                "flows": {
                    sid: {
                        "rtt_ms": f.rtt_ms,
                        "cwnd": f.cwnd,
                        "role": f.role.value,
                        "active": f.active,
                        "inflight_bytes": f.inflight_bytes,
                        "inflight": list(f.inflight),
                    }
                    for sid, f in sorted(self._flows.items())
                },
            }


class _Flow:
    __slots__ = (
        "sid",
        "init_rtt",
        "init_cwnd",
        "role",
        "rtt_ms",
        "cwnd",
        "active",
        "inflight",
        "inflight_bytes",
    )

    def __init__(self, sid, rtt_ms, cwnd, role):
        self.sid = sid
        self.init_rtt = rtt_ms
        self.init_cwnd = cwnd
        self.role = role
        self.rtt_ms = rtt_ms
        self.cwnd = cwnd
        self.active = True
        # (seq, length) in the order actually sent on this subflow.
        self.inflight: deque[tuple[int, int]] = deque()
        self.inflight_bytes = 0
def _is_int(value):
    return isinstance(value, int) and not isinstance(value, bool)


def _is_pos_int(value):
    return _is_int(value) and value > 0


def _is_nonempty_id_value(value):
    return isinstance(value, str) and value != ""


def _require_nonempty_id(value):
    if not _is_nonempty_id_value(value):
        raise SchedulerError(ErrCode.INVALID_PARAM, "subflow id must be a non-empty str")
