package modbus

import (
	"testing"
	"time"

	"github.com/goburrow/serial"
)

// mockSerialPort implements the serial.Port interface for testing.
type mockSerialPort struct{}

func (m *mockSerialPort) Open(*serial.Config) error {
	return nil
}

func (m *mockSerialPort) Read([]byte) (int, error) {
	return 0, nil
}

func (m *mockSerialPort) Write([]byte) (int, error) {
	return 0, nil
}

func (m *mockSerialPort) Close() error {
	return nil
}

func TestSerialPortWrapper(t *testing.T) {
	// Create a configuration for the serial port wrapper.
	conf := &serialPortConfig{
		Device:   "/dev/test", // Device path is irrelevant since we're using a mock.
		Speed:    19200,
		DataBits: 8,
		Parity:   PARITY_NONE,
		StopBits: 2,
	}

	// Create a serialPortWrapper and assign the mock directly.
	spw := &serialPortWrapper{
		conf:     conf,
		port:     &mockSerialPort{},
		deadline: time.Time{},
	}

	// No need to call spw.Open() since the mock port is already set.
	// Test SetDeadline.
	err := spw.SetDeadline(time.Now().Add(time.Second))
	if err != nil {
		t.Errorf("SetDeadline error = %v", err)
	}

	// Test Read.
	n, err := spw.Read(make([]byte, 10))
	if err != nil {
		t.Errorf("Read error = %v", err)
	}
	if n != 0 {
		t.Errorf("Unexpected read len = %d, want 0", n)
	}
}
