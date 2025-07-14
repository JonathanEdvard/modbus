package modbus

import (
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"net"
)

// LoadCertPool loads a certificate store from a file into a CertPool object.
func LoadCertPool(filePath string) (*x509.CertPool, error) {
	buf, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	if len(buf) == 0 {
		return nil, fmt.Errorf("%v: empty file", filePath)
	}

	cp := x509.NewCertPool()
	if !cp.AppendCertsFromPEM(buf) {
		return nil, fmt.Errorf("%v: no certificate found", filePath)
	}

	return cp, nil
}

// tlsSockWrapper wraps a TLS socket to work around odd error handling in
// TLSConn on internal connection state corruption.
// tlsSockWrapper implements the net.Conn interface to allow its
// use by the modbus TCP transport.
type tlsSockWrapper struct {
	sock net.Conn
}

func newTLSSockWrapper(sock net.Conn) *tlsSockWrapper {
	return &tlsSockWrapper{
		sock: sock,
	}
}

func (tsw *tlsSockWrapper) Read(buf []byte) (int, error) {
	return tsw.sock.Read(buf)
}

func (tsw *tlsSockWrapper) Write(buf []byte) (int, error) {
	wlen, err := tsw.sock.Write(buf)
	if err != nil && os.IsTimeout(err) {
		tsw.sock.Close()
	}

	return wlen, err
}

func (tsw *tlsSockWrapper) Close() error {
	return tsw.sock.Close()
}

func (tsw *tlsSockWrapper) SetDeadline(deadline time.Time) error {
	return tsw.sock.SetDeadline(deadline)
}

func (tsw *tlsSockWrapper) SetReadDeadline(deadline time.Time) error {
	return tsw.sock.SetReadDeadline(deadline)
}

func (tsw *tlsSockWrapper) SetWriteDeadline(deadline time.Time) error {
	return tsw.sock.SetWriteDeadline(deadline)
}

func (tsw *tlsSockWrapper) LocalAddr() net.Addr {
	return tsw.sock.LocalAddr()
}

func (tsw *tlsSockWrapper) RemoteAddr() net.Addr {
	return tsw.sock.RemoteAddr()
}
