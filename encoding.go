package modbus

import (
	"encoding/binary"
	"math"
)

func uint16ToBytes(endianness Endianness, in uint16) []byte {
	out := make([]byte, 2)
	switch endianness {
	case BIG_ENDIAN:
		binary.BigEndian.PutUint16(out, in)
	case LITTLE_ENDIAN:
		binary.LittleEndian.PutUint16(out, in)
	}

	return out
}

func uint16sToBytes(endianness Endianness, in []uint16) []byte {
	out := make([]byte, 0, 2*len(in))
	for i := range in {
		out = append(out, uint16ToBytes(endianness, in[i])...)
	}

	return out
}

func bytesToUint16(endianness Endianness, in []byte) uint16 {
	switch endianness {
	case BIG_ENDIAN:
		return binary.BigEndian.Uint16(in)
	case LITTLE_ENDIAN:
		return binary.LittleEndian.Uint16(in)
	}

	return 0
}

func bytesToUint16s(endianness Endianness, in []byte) []uint16 {
	out := make([]uint16, 0, len(in)/2)
	for i := 0; i < len(in); i += 2 {
		out = append(out, bytesToUint16(endianness, in[i:i+2]))
	}

	return out
}

func bytesToUint32s(endianness Endianness, wordOrder WordOrder, in []byte) []uint32 {
	out := make([]uint32, 0, len(in)/4)
	var u32 uint32

	for i := 0; i < len(in); i += 4 {
		switch endianness {
		case BIG_ENDIAN:
			if wordOrder == HIGH_WORD_FIRST {
				u32 = binary.BigEndian.Uint32(in[i : i+4])
			} else {
				u32 = binary.BigEndian.Uint32(
					[]byte{in[i+2], in[i+3], in[i+0], in[i+1]})
			}
		case LITTLE_ENDIAN:
			if wordOrder == LOW_WORD_FIRST {
				u32 = binary.LittleEndian.Uint32(in[i : i+4])
			} else {
				u32 = binary.LittleEndian.Uint32(
					[]byte{in[i+2], in[i+3], in[i+0], in[i+1]})
			}
		}

		out = append(out, u32)
	}

	return out
}

func uint32ToBytes(endianness Endianness, wordOrder WordOrder, in uint32) []byte {
	out := make([]byte, 4)

	switch endianness {
	case BIG_ENDIAN:
		binary.BigEndian.PutUint32(out, in)

		// swap words if needed
		if wordOrder == LOW_WORD_FIRST {
			out[0], out[1], out[2], out[3] = out[2], out[3], out[0], out[1]
		}
	case LITTLE_ENDIAN:
		binary.LittleEndian.PutUint32(out, in)

		// swap words if needed
		if wordOrder == HIGH_WORD_FIRST {
			out[0], out[1], out[2], out[3] = out[2], out[3], out[0], out[1]
		}
	}

	return out
}

func bytesToFloat32s(endianness Endianness, wordOrder WordOrder, in []byte) []float32 {
	u32s := bytesToUint32s(endianness, wordOrder, in)
	out := make([]float32, 0, len(u32s))

	for _, u32 := range u32s {
		out = append(out, math.Float32frombits(u32))
	}

	return out
}

func float32ToBytes(endianness Endianness, wordOrder WordOrder, in float32) []byte {
	return uint32ToBytes(endianness, wordOrder, math.Float32bits(in))
}

func bytesToUint64s(endianness Endianness, wordOrder WordOrder, in []byte) []uint64 {
	out := make([]uint64, 0, len(in)/8)
	var u64 uint64

	for i := 0; i < len(in); i += 8 {
		switch endianness {
		case BIG_ENDIAN:
			if wordOrder == HIGH_WORD_FIRST {
				u64 = binary.BigEndian.Uint64(in[i : i+8])
			} else {
				u64 = binary.BigEndian.Uint64(
					[]byte{in[i+6], in[i+7], in[i+4], in[i+5],
						in[i+2], in[i+3], in[i+0], in[i+1]})
			}
		case LITTLE_ENDIAN:
			if wordOrder == LOW_WORD_FIRST {
				u64 = binary.LittleEndian.Uint64(in[i : i+8])
			} else {
				u64 = binary.LittleEndian.Uint64(
					[]byte{in[i+6], in[i+7], in[i+4], in[i+5],
						in[i+2], in[i+3], in[i+0], in[i+1]})
			}
		}

		out = append(out, u64)
	}

	return out
}

func uint64ToBytes(endianness Endianness, wordOrder WordOrder, in uint64) []byte {
	out := make([]byte, 8)

	switch endianness {
	case BIG_ENDIAN:
		binary.BigEndian.PutUint64(out, in)

		// swap words if needed
		if wordOrder == LOW_WORD_FIRST {
			out[0], out[1], out[2], out[3], out[4], out[5], out[6], out[7] =
				out[6], out[7], out[4], out[5], out[2], out[3], out[0], out[1]
		}
	case LITTLE_ENDIAN:
		binary.LittleEndian.PutUint64(out, in)

		// swap words if needed
		if wordOrder == HIGH_WORD_FIRST {
			out[0], out[1], out[2], out[3], out[4], out[5], out[6], out[7] =
				out[6], out[7], out[4], out[5], out[2], out[3], out[0], out[1]
		}
	}

	return out
}

func bytesToFloat64s(endianness Endianness, wordOrder WordOrder, in []byte) []float64 {
	u64s := bytesToUint64s(endianness, wordOrder, in)
	out := make([]float64, 0, len(u64s))

	for _, u64 := range u64s {
		out = append(out, math.Float64frombits(u64))
	}

	return out
}

func float64ToBytes(endianness Endianness, wordOrder WordOrder, in float64) []byte {
	return uint64ToBytes(endianness, wordOrder, math.Float64bits(in))
}

func encodeBools(in []bool) []byte {
	byteCount := uint(len(in)) / 8
	if len(in)%8 != 0 {
		byteCount++
	}

	out := make([]byte, byteCount)
	for i := uint(0); i < uint(len(in)); i++ {
		if in[i] {
			out[i/8] |= (0x01 << (i % 8))
		}
	}

	return out
}

func decodeBools(quantity uint16, in []byte) []bool {
	out := make([]bool, quantity)
	for i := uint(0); i < uint(quantity); i++ {
		out[i] = (((in[i/8] >> (i % 8)) & 0x01) == 0x01)
	}

	return out
}
