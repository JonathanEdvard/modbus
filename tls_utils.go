package modbus

import (
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"io/ioutil"
	"net"
)

// LoadCertPool loads a certificate store from a file into a CertPool object.
func LoadCertPool(filePath string) (cp *x509.CertPool, err error) {
	var buf []byte

	buf, err = ioutil.ReadFile(filePath)
	if err != nil {
		return
	}

	if len(buf) == 0 {
		err = fmt.Errorf("%v: empty file", filePath)
		return
	}

	cp = x509.NewCertPool()
	if !cp.AppendCertsFromPEM(buf) {
		err = fmt.Errorf("%v: no certificate found", filePath)
		return
	}

	return
}

// tlsSockWrapper wraps a TLS socket to work around odd error handling in
// TLSConn on internal connection state corruption.
// tlsSockWrapper implements the net.Conn interface to allow its
// use by the modbus TCP transport.
type tlsSockWrapper struct {
	sock net.Conn
}

func newTLSSockWrapper(sock net.Conn) (tsw *tlsSockWrapper) {
	tsw = &tlsSockWrapper{
		sock: sock,
	}

	return
}

func (tsw *tlsSockWrapper) Read(buf []byte) (rlen int, err error) {
	rlen, err = tsw.sock.Read(buf)

	return
}

func (tsw *tlsSockWrapper) Write(buf []byte) (wlen int, err error) {
	wlen, err = tsw.sock.Write(buf)

	if err != nil && os.IsTimeout(err) {
		tsw.sock.Close()
	}

	return
}

func (tsw *tlsSockWrapper) Close() (err error) {
	err = tsw.sock.Close()

	return
}

func (tsw *tlsSockWrapper) SetDeadline(deadline time.Time) (err error) {
	err = tsw.sock.SetDeadline(deadline)

	return
}

func (tsw *tlsSockWrapper) SetReadDeadline(deadline time.Time) (err error) {
	err = tsw.sock.SetReadDeadline(deadline)

	return
}

func (tsw *tlsSockWrapper) SetWriteDeadline(deadline time.Time) (err error) {
	err = tsw.sock.SetWriteDeadline(deadline)

	return
}

func (tsw *tlsSockWrapper) LocalAddr() (addr net.Addr) {
	addr = tsw.sock.LocalAddr()

	return
}

func (tsw *tlsSockWrapper) RemoteAddr() (addr net.Addr) {
	addr = tsw.sock.RemoteAddr()

	return
}
