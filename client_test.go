package modbus

import (
	"log/slog"
	"os"
	"testing"
)

type mockTransport struct{}

func (m *mockTransport) Close() error {
	return nil
}

func (m *mockTransport) ExecuteRequest(req *pdu) (*pdu, error) {
	// Simulate response for ReadCoils: byte count 1, data 0xCD (11001101 binary)
	return &pdu{unitId: req.unitId, functionCode: req.functionCode, payload: []byte{1, 0xCD}}, nil
}

func (m *mockTransport) ReadRequest() (*pdu, error) {
	return nil, nil
}

func (m *mockTransport) WriteResponse(*pdu) error {
	return nil
}

func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		conf    *ClientConfiguration
		wantErr bool
	}{
		{
			name: "Valid RTU",
			conf: &ClientConfiguration{URL: "rtu:///dev/ttyUSB0"},
		},
		{
			name:    "Invalid URL",
			conf:    &ClientConfiguration{URL: "invalid"},
			wantErr: true,
		},
		{
			name:    "TCP+TLS missing cert",
			conf:    &ClientConfiguration{URL: "tcp+tls://localhost:502"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClient(tt.conf, slog.New(slog.NewTextHandler(os.Stdout, nil)))
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClient() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestReadCoils(t *testing.T) {
	mc := &ModbusClient{
		transport: &mockTransport{},
		unitId:    1,
	}

	values, err := mc.ReadCoils(0, 8)
	if err != nil {
		t.Errorf("ReadCoils() error = %v", err)
	}
	expected := []bool{true, false, true, true, false, false, true, true}
	for i, v := range values {
		if v != expected[i] {
			t.Errorf("ReadCoils() got = %v, want %v at index %d", v, expected[i], i)
		}
	}
}

type mockTransportWrite struct{}

func (m *mockTransportWrite) Close() error {
	return nil
}

func (m *mockTransportWrite) ExecuteRequest(req *pdu) (*pdu, error) {
	// Echo response for WriteRegister
	return &pdu{unitId: req.unitId, functionCode: req.functionCode, payload: req.payload}, nil
}

func (m *mockTransportWrite) ReadRequest() (*pdu, error) {
	return nil, nil
}

func (m *mockTransportWrite) WriteResponse(*pdu) error {
	return nil
}

func TestWriteRegister(t *testing.T) {
	mc := &ModbusClient{
		transport:  &mockTransportWrite{},
		unitId:     1,
		endianness: BIG_ENDIAN,
	}

	err := mc.WriteRegister(0, 0xABCD)
	if err != nil {
		t.Errorf("WriteRegister() error = %v", err)
	}
}

type mockTransportTimeout struct{}

func (m *mockTransportTimeout) Close() error {
	return nil
}

func (m *mockTransportTimeout) ExecuteRequest(*pdu) (*pdu, error) {
	return nil, os.ErrDeadlineExceeded
}

func (m *mockTransportTimeout) ReadRequest() (*pdu, error) {
	return nil, nil
}

func (m *mockTransportTimeout) WriteResponse(*pdu) error {
	return nil
}

func TestTimeout(t *testing.T) {
	mc := &ModbusClient{
		transport: &mockTransportTimeout{},
		unitId:    1,
	}

	_, err := mc.ReadCoils(0, 1)
	if err != ErrRequestTimedOut {
		t.Errorf("Expected ErrRequestTimedOut, got %v", err)
	}
}
