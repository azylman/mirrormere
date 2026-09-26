package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// MaxPayloadSize protects against heap exhaustion from malformed or malicious frames.
	MaxPayloadSize = 64 * 1024 // 64KB

	// ProtocolVersion0 is the standard Google Cast v2 wire protocol version.
	ProtocolVersion0 = 0

	// PayloadTypeString indicates payload_utf8 is populated.
	PayloadTypeString = 0
	// PayloadTypeBinary indicates payload_binary is populated.
	PayloadTypeBinary = 1
)

var (
	// ErrPayloadTooLarge indicates that a frame length exceeds MaxPayloadSize.
	ErrPayloadTooLarge = errors.New("cast message payload exceeds max size")
	// ErrCorruptFrame indicates that protobuf varint or wire formatting is corrupted.
	ErrCorruptFrame = errors.New("corrupt cast message protobuf frame")
	// ErrTruncatedFrame indicates that the frame bytes were truncated before the field ended.
	ErrTruncatedFrame = errors.New("truncated cast message frame")
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

	// Field 1: protocol_version (varint, tag = 0x08)
	body = appendVarintField(body, 1, uint64(msg.ProtocolVersion), &scratch)

	// Field 2: source_id (length-delimited, tag = 0x12)
	if msg.SourceID != "" {
		body = appendLengthDelimited(body, 2, []byte(msg.SourceID), &scratch)
	}

	// Field 3: destination_id (length-delimited, tag = 0x1a)
	if msg.DestinationID != "" {
		body = appendLengthDelimited(body, 3, []byte(msg.DestinationID), &scratch)
	}

	// Field 4: namespace (length-delimited, tag = 0x22)
	if msg.Namespace != "" {
		body = appendLengthDelimited(body, 4, []byte(msg.Namespace), &scratch)
	}

	// Field 5: payload_type (varint, tag = 0x28)
	body = appendVarintField(body, 5, uint64(msg.PayloadType), &scratch)

	// Field 6: payload_utf8 (length-delimited, tag = 0x32)
	if msg.PayloadUTF8 != "" || msg.PayloadType == PayloadTypeString {
		body = appendLengthDelimited(body, 6, []byte(msg.PayloadUTF8), &scratch)
	}

	// Field 7: payload_binary (length-delimited, tag = 0x3a)
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

		fieldNum := int(tag >> 3)
		wireType := int(tag & 0x07)

		switch wireType {
		case 0: // Varint
			val, vn := binary.Uvarint(payloadBuf[offset:])
			if vn <= 0 {
				return CastMessage{}, ErrCorruptFrame
			}
			offset += vn
			switch fieldNum {
			case 1:
				msg.ProtocolVersion = int(val)
			case 5:
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
	tag := uint64(fieldNum << 3)
	n := binary.PutUvarint(scratch[:], tag)
	dst = append(dst, scratch[:n]...)
	n = binary.PutUvarint(scratch[:], val)
	return append(dst, scratch[:n]...)
}

func appendLengthDelimited(dst []byte, fieldNum int, data []byte, scratch *[10]byte) []byte {
	tag := uint64((fieldNum << 3) | 2)
	n := binary.PutUvarint(scratch[:], tag)
	dst = append(dst, scratch[:n]...)
	n = binary.PutUvarint(scratch[:], uint64(len(data)))
	dst = append(dst, scratch[:n]...)
	return append(dst, data...)
}
