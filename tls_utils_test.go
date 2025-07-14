package modbus

import (
	"os"
	"testing"
	"time"

	"net"
)

func TestLoadCertPool(t *testing.T) {
	pemData := []byte(`-----BEGIN CERTIFICATE-----
MIIBdzCCASOgAwIBAgIBATANBgkqhkiG9w0BAQ0FADBFMQswCQYDVQQGEwJHQjEY
MBYGA1UEChMPU2VjdGlnbyBMaW1pdGVkMRcwFQYDVQQDEw5TZWN0aWdvIFJTQSBD
QQ==
-----END CERTIFICATE-----`)
	tmpFile, err := os.CreateTemp("", "cert.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.Write(pemData); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	_, err = LoadCertPool(tmpFile.Name())
	if err != nil {
		t.Errorf("LoadCertPool error = %v", err)
	}

	emptyFile, err := os.CreateTemp("", "empty.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(emptyFile.Name())
	_, err = LoadCertPool(emptyFile.Name())
	if err == nil {
		t.Errorf("Expected error for empty file")
	}
}

type mockTLSConn struct {
	net.Conn
}

func (m *mockTLSConn) Read(b []byte) (int, error) {
	return 0, nil
}

func (m *mockTLSConn) Write(b []byte) (int, error) {
	return len(b), nil
}

func (m *mockTLSConn) Close() error {
	return nil
}

func (m *mockTLSConn) SetDeadline(t time.Time) error {
	return nil
}

func (m *mockTLSConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (m *mockTLSConn) SetWriteDeadline(t time.Time) error {
	return nil
}

func TestTLSSockWrapper(t *testing.T) {
	tsw := newTLSSockWrapper(&mockTLSConn{})

	n, err := tsw.Write([]byte("test"))
	if err != nil || n != 4 {
		t.Errorf("Write failed: got %d bytes, err %v", n, err)
	}
}
