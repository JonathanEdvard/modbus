package modbus

import (
	"bytes"
	"math"
	"testing"
)

func TestUint16ToBytes(t *testing.T) {
	tests := []struct {
		endianness Endianness
		in         uint16
		want       []byte
	}{
		{BIG_ENDIAN, 0x1234, []byte{0x12, 0x34}},
		{LITTLE_ENDIAN, 0x1234, []byte{0x34, 0x12}},
	}

	for _, tt := range tests {
		got := uint16ToBytes(tt.endianness, tt.in)
		if !bytes.Equal(got, tt.want) {
			t.Errorf("uint16ToBytes(%v, %x) = %v, want %v", tt.endianness, tt.in, got, tt.want)
		}
	}
}

func TestBytesToUint16s(t *testing.T) {
	tests := []struct {
		endianness Endianness
		in         []byte
		want       []uint16
	}{
		{BIG_ENDIAN, []byte{0x12, 0x34, 0x56, 0x78}, []uint16{0x1234, 0x5678}},
		{LITTLE_ENDIAN, []byte{0x34, 0x12, 0x78, 0x56}, []uint16{0x1234, 0x5678}},
	}

	for _, tt := range tests {
		got := bytesToUint16s(tt.endianness, tt.in)
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("bytesToUint16s(%v, %v) = %v, want %v at index %d", tt.endianness, tt.in, got[i], tt.want[i], i)
			}
		}
	}
}

func TestFloat32ToBytes(t *testing.T) {
	in := float32(123.45)
	bits := math.Float32bits(in)
	wantBigHigh := []byte{
		byte(bits >> 24),
		byte(bits >> 16),
		byte(bits >> 8),
		byte(bits),
	}

	got := float32ToBytes(BIG_ENDIAN, HIGH_WORD_FIRST, in)
	if !bytes.Equal(got, wantBigHigh) {
		t.Errorf("float32ToBytes got %v, want %v", got, wantBigHigh)
	}
}
