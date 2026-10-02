// Package rfc5961 implements an RFC 5961 style validator for segments
// arriving on an already established TCP connection. It is a pure,
// deterministic state machine: every sequence number is handled modulo
// 2^32, positions are compared as relative offsets
// (x-base) mod 2^32, and all observable state (sequence numbers and
// challenge counters) is reproducible by replaying the same segment
// trace.
//
// # Processing order
//
// Validator.Process applies the checks in exactly this order and returns
// the first matching disposition:
//
//  1. RST segments are always tested with L=0 regardless of Len/FIN.
//     Out of the receive window: Drop. Seq == rcvNxt: Reset and the
//     connection becomes Closed. Otherwise (in window but not equal to
//     rcvNxt): an RST-triggered challenge ACK via the limiter.
//  2. A non-RST segment whose [Seq, Seq+L) range misses the receive
//     window yields an ordinary AckPlain. Plain ACKs never touch the
//     limiter and never change sequence state.
//  3. An acceptable SYN yields a challenge ACK.
//  4. An acceptable non-SYN segment without ACK is Dropped.
//  5. The ACK number must lie in the closed interval
//     [sndUna-maxSndWnd, sndNxt]; otherwise a challenge ACK is sent.
//  6. Otherwise the segment is Accepted: sndUna advances when the ACK
//     falls in (sndUna, sndNxt], maxSndWnd grows with Wnd, and rcvNxt
//     advances only when the segment starts at or to the left of rcvNxt
//     and its end lies strictly to the right of it. In-window
//     out-of-order data never advances rcvNxt.
//
// # Acceptance rule
//
// With a zero receive window only an empty (L=0) segment with Seq exactly
// equal to rcvNxt is acceptable. Otherwise the first byte is in window
// when (Seq-rcvNxt) mod 2^32 < rcvWnd, or, for L>0, the last byte is in
// window when (Seq+L-1-rcvNxt) mod 2^32 < rcvWnd. Because rcvWnd never
// exceeds 2^30, an offset at or above 2^31 denotes a position strictly
// left of rcvNxt and cannot match the window, which makes wrap-around
// comparisons straightforward.
//
// # Why the ACK range is closed on both ends
//
// The lower bound sndUna-maxSndWnd accepts ACKs that lag behind sndUna
// by at most the largest window the peer ever advertised: such an ACK
// could be a legitimate retransmission of older feedback, so it must be
// processed rather than challenged, hence the lower end is included.
// The upper bound sndNxt acknowledges the most recent byte the local
// side has sent (sndNxt is "next to send", so sndNxt-1 is the last
// outstanding byte and sndNxt itself is a valid cumulative ACK value),
// hence the upper end is included as well.
//
// # Challenge ACK rate limiter
//
// The limiter keeps a fixed window starting at ws, a total challenge
// count cnt and an RST-triggered count cntR. It advances only when a
// challenge is actually requested (Drops and plain ACKs never touch it).
// When the window is empty or now-ws >= P, a fresh window starts at now
// with both counters zero. A challenge is sent only when cnt < C and,
// for RST triggers, cntR < ceil(C/2). Consequently at most C challenges
// are sent per window and at most ceil(C/2) of them are RST-triggered;
// the remaining slots stay available for SYN and ACK-range challenges.
// A Suppressed challenge changes neither ws, cnt nor cntR.
//
// # Rejections and concurrency
//
// Rejected calls report, in this order: invalid constructor or call
// parameters (now or segment fields out of range, SYN and RST together),
// clock regression (now earlier than the last accepted call), then a
// Closed connection. A rejected call changes no state at all, including
// the clock and the limiter. All six dispositions are accepted calls and
// advance the clock. All methods are serialized through a single mutex,
// so concurrent behavior is equivalent to some serial ordering.
package rfc5961
