"""Error taxonomy with a fixed, documented priority."""

from __future__ import annotations

import enum


class ErrCode(enum.Enum):
    # Fixed rejection priority (lower value => checked/reported first):
    INVALID_PARAM = "invalid_param"
    SUBFLOW_NOT_FOUND = "subflow_not_found"
    STALE_ACK = "stale_ack"
    ACK_NOT_SEGMENT_ALIGNED = "ack_not_segment_aligned"
    ACK_OUT_OF_RANGE = "ack_out_of_range"


class SchedulerError(Exception):
    """A rejected event. No state change is observable after it is raised."""

    def __init__(self, code: ErrCode, message: str = ""):
        super().__init__(code.value + (": " + message if message else ""))
        self.code = code
        self.message = message
