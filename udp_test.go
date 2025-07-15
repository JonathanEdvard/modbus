package modbus

import (
	"net"
	"testing"
	"time"
)

// mockUDPConn implements net.Conn for testing udpSockWrapper without real network calls.
type mockUDPConn struct {
	readBuf  []byte // Buffer for simulated reads
	writeBuf []byte // Buffer for simulated writes
}

func (m *mockUDPConn) Read(b []byte) (int, error) {
	if len(m.readBuf) == 0 {
		return 0, nil // Simulate no data available
	}
	n := copy(b, m.readBuf)
	m.readBuf = m.readBuf[n:]
	return n, nil
}

func (m *mockUDPConn) Write(b []byte) (int, error) {
	m.writeBuf = append(m.writeBuf, b...)
	return len(b), nil
}

func (m *mockUDPConn) Close() error {
	return nil
}

func (m *mockUDPConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
}

func (m *mockUDPConn) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}
}

func (m *mockUDPConn) SetDeadline(t time.Time) error {
	return nil
}

func (m *mockUDPConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (m *mockUDPConn) SetWriteDeadline(t time.Time) error {
	return nil
}

func TestUDPSockWrapper(t *testing.T) {
	// Use a mock UDP connection instead of a real network socket
	conn := &mockUDPConn{}

	usw := newUDPSockWrapper(conn)

	// Test Write
	n, err := usw.Write([]byte("test"))
	if err != nil {
		t.Errorf("Write error = %v", err)
	}
	if n != 4 {
		t.Errorf("Write len = %d, want 4", n)
	}

	// Test Read (simulate no data available)
	buf := make([]byte, 10)
	n, err = usw.Read(buf)
	if err != nil {
		t.Errorf("Read error = %v", err)
	}
	if n != 0 {
		t.Errorf("Unexpected read data: got %d bytes, want 0", n)
	}
}
