package ws

// DecodeWhole is a deliberately simple, whole-buffer reference decoder.
//
// It performs the same protocol checks as Decoder but walks the complete stream
// in one pass, without any incremental buffering. It exists so tests can prove
// that the incremental decoder's events, error reason and error offset do not
// depend on how the input is chunked.
//
// Trailing bytes that do not complete a frame are ignored: the incremental
// decoder likewise holds them back without delivering or failing.
func DecodeWhole(data []byte, maxMessage int) ([]Event, error) {
	var events []Event

	var frag bool
	var fragKind int
	var msg []byte

	off := 0
	for off < len(data) {
		frameStart := off
		b := data[off:]
		if len(b) < 2 {
			break
		}

		b0, b1 := b[0], b[1]
		fin := b0&bitFin != 0
		opcode := int(b0 & 0x0f)

		if b0&bitsRSV != 0 {
			return events, wholeErr(ErrRSV, frameStart)
		}
		if !validOpcode(opcode) {
			return events, wholeErr(ErrOpcode, frameStart)
		}

		control := opcode >= 8
		dataFrame := opcode >= 1 && opcode <= 2

		if control && !fin {
			return events, wholeErr(ErrControlFin, frameStart)
		}
		if opcode == 0 && !frag {
			return events, wholeErr(ErrContinuationOutside, frameStart)
		}
		if dataFrame && frag {
			return events, wholeErr(ErrNewDataInFragment, frameStart)
		}

		if b1&bitMask == 0 {
			return events, wholeErr(ErrUnmasked, frameStart+1)
		}

		indicator := int64(b1 & 0x7f)
		if control && indicator > 125 {
			return events, wholeErr(ErrControlTooLong, frameStart+1)
		}

		payloadLen := indicator
		lenEnd := frameStart + 1
		switch indicator {
		case 126:
			if len(b) < 4 {
				return events, nil
			}
			payloadLen = int64(b[2])<<8 | int64(b[3])
			if payloadLen < 126 {
				return events, wholeErr(ErrLengthEncoding, frameStart+2)
			}
			lenEnd = frameStart + 3
		case 127:
			if len(b) < 10 {
				return events, nil
			}
			if b[2]&0x80 != 0 {
				return events, wholeErr(ErrLength64Bit, frameStart+2)
			}
			for i := 2; i < 10; i++ {
				payloadLen = payloadLen<<8 | int64(b[i])
			}
			if payloadLen <= 65535 {
				return events, wholeErr(ErrLengthEncoding, frameStart+2)
			}
			lenEnd = frameStart + 9
		}

		if !control {
			if int64(len(msg))+payloadLen > int64(maxMessage) {
				return events, wholeErr(ErrMessageTooLarge, lenEnd)
			}
		} else if opcode == 8 && payloadLen == 1 {
			return events, wholeErr(ErrCloseLength, lenEnd)
		}

		headerLen := lenEnd - frameStart + 1
		if len(b) < headerLen+4+int(payloadLen) {
			return events, nil
		}
		maskKey := b[headerLen : headerLen+4]
		headerLen += 4

		payload := make([]byte, payloadLen)
		for i := int64(0); i < payloadLen; i++ {
			payload[i] = b[int64(headerLen)+i] ^ maskKey[i&3]
		}

		switch opcode {
		case 8:
			if payloadLen >= 2 {
				code := int(payload[0])<<8 | int(payload[1])
				if !validCloseCode(code) {
					return events, wholeErr(ErrCloseCode, frameStart+headerLen)
				}
			}
			events = append(events, Event{Kind: EventClose, Data: payload})
			return events, nil
		case 9, 10:
			kind := EventPing
			if opcode == 10 {
				kind = EventPong
			}
			events = append(events, Event{Kind: kind, Data: payload})
		default:
			msg = append(msg, payload...)
			if !fin {
				frag = true
				fragKind = opcode
				break
			}
			kind := opcode
			if opcode == 0 {
				kind = fragKind
				frag = false
				fragKind = 0
			}
			e := Event{Data: msg}
			msg = nil
			if kind == 1 {
				e.Kind = EventText
			} else {
				e.Kind = EventBinary
			}
			events = append(events, e)
		}

		off = frameStart + headerLen + int(payloadLen)
	}
	return events, nil
}

func wholeErr(err error, offset int) error {
	return &FrameError{Err: err, Offset: int64(offset)}
}
