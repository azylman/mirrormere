package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func makeFrame(hdrLen uint32, payload []byte) *bytes.Buffer {
	buf := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(buf[:4], hdrLen)
	copy(buf[4:], payload)
	return bytes.NewBuffer(buf)
}

func TestEncodeDecodeCastMessage_Roundtrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		msg  CastMessage
	}{
		{
			name: "string_payload",
			msg: CastMessage{
				ProtocolVersion: ProtocolVersion0,
				SourceID:        "sender-0",
				DestinationID:   "receiver-0",
				Namespace:       "urn:x-cast:com.google.cast.tp.connection",
				PayloadType:     PayloadTypeString,
				PayloadUTF8:     `{"type":"CONNECT"}`,
			},
		},
		{
			name: "binary_payload",
			msg: CastMessage{
				ProtocolVersion: ProtocolVersion0,
				SourceID:        "client-1",
				DestinationID:   "device-1",
				Namespace:       "urn:x-cast:com.google.cast.media",
				PayloadType:     PayloadTypeBinary,
				PayloadBinary:   []byte{0xDE, 0xAD, 0xBE, 0xEF},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := EncodeCastMessage(tc.msg)
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
			if decoded.ProtocolVersion != tc.msg.ProtocolVersion || decoded.SourceID != tc.msg.SourceID ||
				decoded.DestinationID != tc.msg.DestinationID || decoded.Namespace != tc.msg.Namespace ||
				decoded.PayloadType != tc.msg.PayloadType || decoded.PayloadUTF8 != tc.msg.PayloadUTF8 ||
				!bytes.Equal(decoded.PayloadBinary, tc.msg.PayloadBinary) {
				t.Fatalf("mismatch: got %+v, want %+v", decoded, tc.msg)
			}
		})
	}
}

func TestEncodeDecodeCastMessage_Errors(t *testing.T) {
	t.Parallel()
	bigPayload := make([]byte, MaxPayloadSize+10)
	if _, err := EncodeCastMessage(CastMessage{PayloadType: PayloadTypeString, PayloadUTF8: string(bigPayload)}); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}

	tests := []struct {
		name    string
		reader  io.Reader
		wantErr error
	}{
		{"length_too_large", makeFrame(MaxPayloadSize+1, nil), ErrPayloadTooLarge},
		{"short_header", bytes.NewReader([]byte{0x00, 0x01}), io.ErrUnexpectedEOF},
		{"short_payload", makeFrame(10, []byte{0x08, 0x00}), io.ErrUnexpectedEOF},
		{"corrupt_varint_tag", makeFrame(1, []byte{0x80}), ErrCorruptFrame},
		{"corrupt_varint_val", makeFrame(2, []byte{0x08, 0x80}), ErrCorruptFrame},
		{"corrupt_length_delimited_len", makeFrame(2, []byte{0x12, 0x80}), ErrCorruptFrame},
		{"truncated_length_delimited", makeFrame(4, []byte{0x12, 0x05, 'a', 'b'}), ErrTruncatedFrame},
		{"giant_uint64_length_delimited", makeFrame(11, []byte{0x12, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F, 'a'}), ErrTruncatedFrame},
		{"truncated_64bit", makeFrame(4, []byte{0x41, 1, 2, 3}), ErrTruncatedFrame},
		{"truncated_32bit", makeFrame(3, []byte{0x4d, 1, 2}), ErrTruncatedFrame},
		{"unsupported_wire_type", makeFrame(2, []byte{0x53, 0x00}), ErrCorruptFrame},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeCastMessage(tc.reader)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got err %v, want %v", err, tc.wantErr)
			}
		})
	}

	// Skip fixed 64-bit and 32-bit wire types
	skipPayload := []byte{0x41, 1, 2, 3, 4, 5, 6, 7, 8, 0x4d, 1, 2, 3, 4, 0x12, 0x04, 't', 'e', 's', 't'}
	msg, err := DecodeCastMessage(makeFrame(uint32(len(skipPayload)), skipPayload))
	if err != nil || msg.SourceID != "test" {
		t.Fatalf("expected SourceID 'test', got %q, err: %v", msg.SourceID, err)
	}
}
