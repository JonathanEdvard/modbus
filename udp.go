package modbus

import (
	"net"
	"time"
)

// udpSockWrapper wraps a net.Conn (typically a UDP connection) to
// allow transports to consume data off the network socket on
// a byte-by-byte basis rather than datagram by datagram.
type udpSockWrapper struct {
	leftoverCount int
	rxbuf         []byte
	sock          net.Conn
}

func newUDPSockWrapper(sock net.Conn) *udpSockWrapper {
	return &udpSockWrapper{
		rxbuf: make([]byte, maxTCPFrameLength),
		sock:  sock,
	}
}

func (usw *udpSockWrapper) Read(buf []byte) (int, error) {
	var copied int
	var rlen int
	var err error

	if usw.leftoverCount > 0 {
		copied = copy(buf, usw.rxbuf[0:usw.leftoverCount])

		if usw.leftoverCount > copied {
			copy(usw.rxbuf, usw.rxbuf[copied:usw.leftoverCount])
		}
		usw.leftoverCount -= copied
	} else {
		rlen, err = usw.sock.Read(usw.rxbuf)
		if err != nil {
			return 0, err
		}
		copied = copy(buf, usw.rxbuf[0:rlen])

		if rlen > copied {
			copy(usw.rxbuf, usw.rxbuf[copied:rlen])
		}
		usw.leftoverCount = rlen - copied
	}

	return copied, nil
}

func (usw *udpSockWrapper) Close() error {
	return usw.sock.Close()
}

func (usw *udpSockWrapper) Write(buf []byte) (int, error) {
	return usw.sock.Write(buf)
}

func (usw *udpSockWrapper) SetDeadline(deadline time.Time) error {
	return usw.sock.SetDeadline(deadline)
}

func (usw *udpSockWrapper) SetReadDeadline(deadline time.Time) error {
	return usw.sock.SetReadDeadline(deadline)
}

func (usw *udpSockWrapper) SetWriteDeadline(deadline time.Time) error {
	return usw.sock.SetWriteDeadline(deadline)
}

func (usw *udpSockWrapper) LocalAddr() net.Addr {
	return usw.sock.LocalAddr()
}

func (usw *udpSockWrapper) RemoteAddr() net.Addr {
	return usw.sock.RemoteAddr()
}
