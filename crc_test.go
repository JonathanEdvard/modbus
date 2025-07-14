package modbus

import (
	"bytes"
	"testing"
)

func TestCRC(t *testing.T) {
	c := crc{}
	c.init()
	c.add([]byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x02})
	got := c.value()
	want := []byte{0xC4, 0x0B}
	if !bytes.Equal(got, want) {
		t.Errorf("CRC value = %X, want %X", got, want)
	}

	if !c.isEqual(0xC4, 0x0B) {
		t.Errorf("isEqual false for correct CRC")
	}

	d := crc{}
	d.init()
	d.add([]byte{0x01, 0x04, 0x02, 0xFF, 0xFF})
	got = d.value()
	want = []byte{0xB8, 0x80}
	if !bytes.Equal(got, want) {
		t.Errorf("CRC value = %X, want %X", got, want)
	}

	if !d.isEqual(0xB8, 0x80) {
		t.Errorf("isEqual false for correct CRC")
	}
}
