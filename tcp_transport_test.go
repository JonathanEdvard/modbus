package modbus

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

type mockConn struct {
	net.Conn
	readBuf bytes.Buffer
}

func (m *mockConn) Write(b []byte) (int, error) {
	return len(b), nil
}

func (m *mockConn) Read(b []byte) (int, error) {
	return m.readBuf.Read(b)
}

func (m *mockConn) Close() error {
	return nil
}

func (m *mockConn) SetDeadline(time.Time) error {
	return nil
}

func TestTCPTransport(t *testing.T) {
	mock := &mockConn{}
	// Corrected response: MBAP header (7 bytes) + PDU (function code 3, byte count 2, one 16-bit register value)
	mock.readBuf.Write([]byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x04, 0x01, 0x03, 0x02, 0x00, 0x01})

	// Provide a no-op logger to avoid nil pointer issues
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tt := newTCPTransport(mock, time.Second, logger)

	req := &pdu{unitId: 1, functionCode: 3, payload: []byte{0x00, 0x00, 0x00, 0x01}}
	res, err := tt.ExecuteRequest(req)
	if err != nil {
		t.Errorf("ExecuteRequest error = %v", err)
	}
	if res.functionCode != 3 || len(res.payload) != 3 {
		t.Errorf("Unexpected response: got %v, want functionCode=3, payload=[2 0 1]", res)
	}

	if !bytes.Equal(res.payload, []byte{0x02, 0x00, 0x01}) {
		t.Errorf("Unexpected payload: got %v, want [2 0 1]", res.payload)
	}
}
