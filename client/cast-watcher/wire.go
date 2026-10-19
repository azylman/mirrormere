package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	MaxPayloadSize    = 64 * 1024 // 64KB
	ProtocolVersion0  = 0
	PayloadTypeString = 0
	PayloadTypeBinary = 1
)

var (
	ErrPayloadTooLarge = errors.New("cast message payload exceeds max size")
	ErrCorruptFrame    = errors.New("corrupt cast message protobuf frame")
	ErrTruncatedFrame  = errors.New("truncated cast message frame")
)

// CastMessage represents a decoded Google Cast v2 wire message.
type CastMessage struct {
	ProtocolVersion int
	SourceID        string
	DestinationID   string
	Namespace       string
	PayloadType     int
	PayloadUTF8     string
	PayloadBinary   []byte
}

// EncodeCastMessage encodes msg into a 4-byte big-endian length-prefixed protobuf payload.
func EncodeCastMessage(msg CastMessage) ([]byte, error) {
	var body []byte
	var scratch [10]byte
	body = appendVarintField(body, 1, uint64(msg.ProtocolVersion), &scratch)
	if msg.SourceID != "" {
		body = appendLengthDelimited(body, 2, []byte(msg.SourceID), &scratch)
	}
	if msg.DestinationID != "" {
		body = appendLengthDelimited(body, 3, []byte(msg.DestinationID), &scratch)
	}
	if msg.Namespace != "" {
		body = appendLengthDelimited(body, 4, []byte(msg.Namespace), &scratch)
	}
	body = appendVarintField(body, 5, uint64(msg.PayloadType), &scratch)
	if msg.PayloadUTF8 != "" || msg.PayloadType == PayloadTypeString {
		body = appendLengthDelimited(body, 6, []byte(msg.PayloadUTF8), &scratch)
	}
	if len(msg.PayloadBinary) > 0 {
		body = appendLengthDelimited(body, 7, msg.PayloadBinary, &scratch)
	}
	if len(body) > MaxPayloadSize {
		return nil, ErrPayloadTooLarge
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	return frame, nil
}

// DecodeCastMessage reads and decodes a single 4-byte framed CastMessage from r.
func DecodeCastMessage(r io.Reader) (CastMessage, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return CastMessage{}, err
	}
	payloadLen := binary.BigEndian.Uint32(lenBuf[:])
	if payloadLen > MaxPayloadSize {
		return CastMessage{}, ErrPayloadTooLarge
	}
	payloadBuf := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payloadBuf); err != nil {
		return CastMessage{}, err
	}

	var msg CastMessage
	offset := 0
	for offset < len(payloadBuf) {
		tag, n := binary.Uvarint(payloadBuf[offset:])
		if n <= 0 {
			return CastMessage{}, ErrCorruptFrame
		}
		offset += n
		fieldNum, wireType := int(tag>>3), int(tag&0x07)

		switch wireType {
		case 0: // Varint
			val, vn := binary.Uvarint(payloadBuf[offset:])
			if vn <= 0 {
				return CastMessage{}, ErrCorruptFrame
			}
			offset += vn
			if fieldNum == 1 {
				msg.ProtocolVersion = int(val)
			} else if fieldNum == 5 {
				msg.PayloadType = int(val)
			}
		case 1: // 64-bit fixed
			if offset+8 > len(payloadBuf) {
				return CastMessage{}, ErrTruncatedFrame
			}
			offset += 8
		case 2: // Length-delimited (string / bytes)
			fieldLen, ln := binary.Uvarint(payloadBuf[offset:])
			if ln <= 0 {
				return CastMessage{}, ErrCorruptFrame
			}
			offset += ln
			if fieldLen > uint64(len(payloadBuf)-offset) {
				return CastMessage{}, ErrTruncatedFrame
			}
			data := payloadBuf[offset : offset+int(fieldLen)]
			offset += int(fieldLen)
			switch fieldNum {
			case 2:
				msg.SourceID = string(data)
			case 3:
				msg.DestinationID = string(data)
			case 4:
				msg.Namespace = string(data)
			case 6:
				msg.PayloadUTF8 = string(data)
			case 7:
				msg.PayloadBinary = append([]byte(nil), data...)
			}
		case 5: // 32-bit fixed
			if offset+4 > len(payloadBuf) {
				return CastMessage{}, ErrTruncatedFrame
			}
			offset += 4
		default:
			return CastMessage{}, fmt.Errorf("%w: unsupported wire type %d", ErrCorruptFrame, wireType)
		}
	}
	return msg, nil
}

func appendVarintField(dst []byte, fieldNum int, val uint64, scratch *[10]byte) []byte {
	n := binary.PutUvarint(scratch[:], uint64(fieldNum<<3))
	dst = append(dst, scratch[:n]...)
	n = binary.PutUvarint(scratch[:], val)
	return append(dst, scratch[:n]...)
}

func appendLengthDelimited(dst []byte, fieldNum int, data []byte, scratch *[10]byte) []byte {
	n := binary.PutUvarint(scratch[:], uint64((fieldNum<<3)|2))
	dst = append(dst, scratch[:n]...)
	n = binary.PutUvarint(scratch[:], uint64(len(data)))
	dst = append(dst, scratch[:n]...)
	return append(dst, data...)
}
