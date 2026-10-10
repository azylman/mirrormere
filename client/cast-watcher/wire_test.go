package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestEncodeDecodeCastMessage_Roundtrip(t *testing.T) {
	orig := CastMessage{
		ProtocolVersion: ProtocolVersion0,
		SourceID:        "sender-0",
		DestinationID:   "receiver-0",
		Namespace:       "urn:x-cast:com.google.cast.tp.connection",
		PayloadType:     PayloadTypeString,
		PayloadUTF8:     `{"type":"CONNECT"}`,
	}

	encoded, err := EncodeCastMessage(orig)
	if err != nil {
		t.Fatalf("EncodeCastMessage failed: %v", err)
	}

	if len(encoded) < 4 {
		t.Fatalf("encoded length too short: %d", len(encoded))
	}

	length := binary.BigEndian.Uint32(encoded[:4])
	if int(length) != len(encoded)-4 {
		t.Errorf("length prefix mismatch: expected %d, got %d", len(encoded)-4, length)
	}

	decoded, err := DecodeCastMessage(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("DecodeCastMessage failed: %v", err)
	}

	if decoded.ProtocolVersion != orig.ProtocolVersion {
		t.Errorf("expected ProtocolVersion %d, got %d", orig.ProtocolVersion, decoded.ProtocolVersion)
	}
	if decoded.SourceID != orig.SourceID {
		t.Errorf("expected SourceID %s, got %s", orig.SourceID, decoded.SourceID)
	}
	if decoded.DestinationID != orig.DestinationID {
		t.Errorf("expected DestinationID %s, got %s", orig.DestinationID, decoded.DestinationID)
	}
	if decoded.Namespace != orig.Namespace {
		t.Errorf("expected Namespace %s, got %s", orig.Namespace, decoded.Namespace)
	}
	if decoded.PayloadType != orig.PayloadType {
		t.Errorf("expected PayloadType %d, got %d", orig.PayloadType, decoded.PayloadType)
	}
	if decoded.PayloadUTF8 != orig.PayloadUTF8 {
		t.Errorf("expected PayloadUTF8 %s, got %s", orig.PayloadUTF8, decoded.PayloadUTF8)
	}
}

func TestEncodeDecodeCastMessage_BinaryPayload(t *testing.T) {
	orig := CastMessage{
		ProtocolVersion: ProtocolVersion0,
		SourceID:        "client-1",
		DestinationID:   "device-1",
		Namespace:       "urn:x-cast:com.google.cast.media",
		PayloadType:     PayloadTypeBinary,
		PayloadBinary:   []byte{0xDE, 0xAD, 0xBE, 0xEF},
	}

	encoded, err := EncodeCastMessage(orig)
	if err != nil {
		t.Fatalf("EncodeCastMessage failed: %v", err)
	}

	decoded, err := DecodeCastMessage(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("DecodeCastMessage failed: %v", err)
	}

	if !bytes.Equal(decoded.PayloadBinary, orig.PayloadBinary) {
		t.Errorf("expected PayloadBinary %v, got %v", orig.PayloadBinary, decoded.PayloadBinary)
	}
}

func TestEncodeCastMessage_TooLarge(t *testing.T) {
	bigPayload := make([]byte, MaxPayloadSize+10)
	msg := CastMessage{
		ProtocolVersion: ProtocolVersion0,
		PayloadType:     PayloadTypeString,
		PayloadUTF8:     string(bigPayload),
	}

	_, err := EncodeCastMessage(msg)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestDecodeCastMessage_LengthTooLarge(t *testing.T) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], MaxPayloadSize+1)

	_, err := DecodeCastMessage(bytes.NewReader(buf[:]))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestDecodeCastMessage_ShortLengthHeader(t *testing.T) {
	_, err := DecodeCastMessage(bytes.NewReader([]byte{0x00, 0x01}))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestDecodeCastMessage_ShortPayload(t *testing.T) {
	var buf [6]byte
	binary.BigEndian.PutUint32(buf[:4], 10) // Claims 10 bytes payload, but only 2 follow
	copy(buf[4:], []byte{0x08, 0x00})

	_, err := DecodeCastMessage(bytes.NewReader(buf[:]))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestDecodeCastMessage_CorruptVarint(t *testing.T) {
	// A tag byte with MSB set but no follow byte
	payload := []byte{0x80}
	var frame bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	frame.Write(lenBuf[:])
	frame.Write(payload)

	_, err := DecodeCastMessage(&frame)
	if !errors.Is(err, ErrCorruptFrame) {
		t.Fatalf("expected ErrCorruptFrame, got %v", err)
	}
}

func TestDecodeCastMessage_CorruptVarintValue(t *testing.T) {
	// Field 1 (tag 0x08, wireType 0) followed by invalid varint (0x80)
	payload := []byte{0x08, 0x80}
	var frame bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	frame.Write(lenBuf[:])
	frame.Write(payload)

	_, err := DecodeCastMessage(&frame)
	if !errors.Is(err, ErrCorruptFrame) {
		t.Fatalf("expected ErrCorruptFrame, got %v", err)
	}
}

func TestDecodeCastMessage_CorruptLengthDelimitedLen(t *testing.T) {
	// Field 2 (tag 0x12, wireType 2) followed by invalid length varint (0x80)
	payload := []byte{0x12, 0x80}
	var frame bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	frame.Write(lenBuf[:])
	frame.Write(payload)

	_, err := DecodeCastMessage(&frame)
	if !errors.Is(err, ErrCorruptFrame) {
		t.Fatalf("expected ErrCorruptFrame, got %v", err)
	}
}

func TestDecodeCastMessage_TruncatedLengthDelimited(t *testing.T) {
	// Field 2 (tag 0x12, wireType 2), length 5, but only 2 bytes present
	payload := []byte{0x12, 0x05, 'a', 'b'}
	var frame bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	frame.Write(lenBuf[:])
	frame.Write(payload)

	_, err := DecodeCastMessage(&frame)
	if !errors.Is(err, ErrTruncatedFrame) {
		t.Fatalf("expected ErrTruncatedFrame, got %v", err)
	}
}

func TestDecodeCastMessage_GiantUint64LengthDelimited(t *testing.T) {
	// Field 2 (tag 0x12, wireType 2) followed by a giant varint length that would overflow int: 0xFF, 0xFF, ...
	payload := []byte{0x12, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F, 'a'}
	var frame bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	frame.Write(lenBuf[:])
	frame.Write(payload)

	_, err := DecodeCastMessage(&frame)
	if !errors.Is(err, ErrTruncatedFrame) {
		t.Fatalf("expected ErrTruncatedFrame on giant length, got %v", err)
	}
}

func TestDecodeCastMessage_SkipFixedWireTypes(t *testing.T) {
	// Include unknown 64-bit field (tag = (8 << 3) | 1 = 0x41) and unknown 32-bit field (tag = (9 << 3) | 5 = 0x4d)
	var payload []byte
	// Field 8 (64-bit fixed, 8 bytes)
	payload = append(payload, 0x41)
	payload = append(payload, []byte{1, 2, 3, 4, 5, 6, 7, 8}...)
	// Field 9 (32-bit fixed, 4 bytes)
	payload = append(payload, 0x4d)
	payload = append(payload, []byte{1, 2, 3, 4}...)
	// Field 2 (source_id = "test")
	payload = append(payload, 0x12, 0x04, 't', 'e', 's', 't')

	var frame bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	frame.Write(lenBuf[:])
	frame.Write(payload)

	msg, err := DecodeCastMessage(&frame)
	if err != nil {
		t.Fatalf("unexpected error skipping fixed types: %v", err)
	}
	if msg.SourceID != "test" {
		t.Errorf("expected SourceID 'test', got %s", msg.SourceID)
	}
}

func TestDecodeCastMessage_TruncatedFixedWireTypes(t *testing.T) {
	// Field 8 (64-bit fixed, requires 8 bytes, only give 3)
	payload := []byte{0x41, 1, 2, 3}
	var frame bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	frame.Write(lenBuf[:])
	frame.Write(payload)

	_, err := DecodeCastMessage(&frame)
	if !errors.Is(err, ErrTruncatedFrame) {
		t.Fatalf("expected ErrTruncatedFrame for truncated 64-bit, got %v", err)
	}

	// Field 9 (32-bit fixed, requires 4 bytes, only give 2)
	payload32 := []byte{0x4d, 1, 2}
	frame.Reset()
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload32)))
	frame.Write(lenBuf[:])
	frame.Write(payload32)

	_, err = DecodeCastMessage(&frame)
	if !errors.Is(err, ErrTruncatedFrame) {
		t.Fatalf("expected ErrTruncatedFrame for truncated 32-bit, got %v", err)
	}
}

func TestDecodeCastMessage_UnsupportedWireType(t *testing.T) {
	// Wire type 3 (start group, tag = (10 << 3) | 3 = 0x53)
	payload := []byte{0x53, 0x00}
	var frame bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	frame.Write(lenBuf[:])
	frame.Write(payload)

	_, err := DecodeCastMessage(&frame)
	if !errors.Is(err, ErrCorruptFrame) {
		t.Fatalf("expected ErrCorruptFrame, got %v", err)
	}
}
