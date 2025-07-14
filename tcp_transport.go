package modbus

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"
)

const (
	maxTCPFrameLength int = 260
	mbapHeaderLength  int = 7
)

type tcpTransport struct {
	logger    *slog.Logger
	socket    net.Conn
	timeout   time.Duration
	lastTxnId uint16
}

// Returns a new TCP transport.
func newTCPTransport(socket net.Conn, timeout time.Duration, logger *slog.Logger) (tt *tcpTransport) {
	tt = &tcpTransport{
		socket:  socket,
		timeout: timeout,
		logger:  logger,
	}

	return
}

// Closes the underlying tcp socket.
func (tt *tcpTransport) Close() (err error) {
	err = tt.socket.Close()

	return
}

// Runs a request across the socket and returns a response.
func (tt *tcpTransport) ExecuteRequest(req *pdu) (res *pdu, err error) {
	err = tt.socket.SetDeadline(time.Now().Add(tt.timeout))
	if err != nil {
		return
	}

	tt.lastTxnId++

	_, err = tt.socket.Write(tt.assembleMBAPFrame(tt.lastTxnId, req))
	if err != nil {
		return
	}

	res, err = tt.readResponse()

	return
}

// Reads a request from the socket.
func (tt *tcpTransport) ReadRequest() (req *pdu, err error) {
	var txnId uint16

	err = tt.socket.SetDeadline(time.Now().Add(tt.timeout))
	if err != nil {
		return
	}

	req, txnId, err = tt.readMBAPFrame()
	if err != nil {
		return
	}

	tt.lastTxnId = txnId

	return
}

// Writes a response to the socket.
func (tt *tcpTransport) WriteResponse(res *pdu) (err error) {
	_, err = tt.socket.Write(tt.assembleMBAPFrame(tt.lastTxnId, res))
	if err != nil {
		return
	}

	return
}

// Reads as many MBAP+modbus frames as necessary until either the response
// matching tt.lastTxnId is received or an error occurs.
func (tt *tcpTransport) readResponse() (res *pdu, err error) {
	for {
		res, _, err = tt.readMBAPFrame()

		if err == ErrUnknownProtocolId {
			continue
		}

		if err != nil {
			return
		}

		break
	}

	return
}

// Reads an entire frame (MBAP header + modbus PDU) from the socket.
func (tt *tcpTransport) readMBAPFrame() (p *pdu, txnId uint16, err error) {
	var rxbuf []byte
	var bytesNeeded int
	var protocolId uint16
	var unitId uint8

	rxbuf = make([]byte, mbapHeaderLength)
	_, err = io.ReadFull(tt.socket, rxbuf)
	if err != nil {
		return
	}

	txnId = bytesToUint16(BIG_ENDIAN, rxbuf[0:2])
	protocolId = bytesToUint16(BIG_ENDIAN, rxbuf[2:4])
	unitId = rxbuf[6]

	bytesNeeded = int(bytesToUint16(BIG_ENDIAN, rxbuf[4:6]))

	bytesNeeded--

	if bytesNeeded+mbapHeaderLength > maxTCPFrameLength {
		err = ErrProtocolError
		return
	}

	if bytesNeeded <= 0 {
		err = ErrProtocolError
		return
	}

	rxbuf = make([]byte, bytesNeeded)
	_, err = io.ReadFull(tt.socket, rxbuf)
	if err != nil {
		return
	}

	if protocolId != 0x0000 {
		err = ErrUnknownProtocolId
		if tt.logger != nil {
			tt.logger.Warn("received unexpected protocol id", "protocolId", fmt.Sprintf("%04X", protocolId))
		}
		return
	}

	p = &pdu{
		unitId:       unitId,
		functionCode: rxbuf[0],
		payload:      rxbuf[1:],
	}

	return
}

// Turns a PDU into an MBAP frame (MBAP header + PDU) and returns it as bytes.
func (tt *tcpTransport) assembleMBAPFrame(txnId uint16, p *pdu) (payload []byte) {
	payload = make([]byte, 0, mbapHeaderLength+1+1+len(p.payload))
	payload = append(payload, uint16ToBytes(BIG_ENDIAN, txnId)...)
	payload = append(payload, 0x00, 0x00)
	payload = append(payload, uint16ToBytes(BIG_ENDIAN, uint16(2+len(p.payload)))...)
	payload = append(payload, p.unitId)
	payload = append(payload, p.functionCode)
	payload = append(payload, p.payload...)

	return
}
