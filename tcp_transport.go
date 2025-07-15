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
	socket    net.Conn
	timeout   time.Duration
	logger    *slog.Logger
	lastTxnId uint16
}

// newTCPTransport creates a new TCP transport with the given socket, timeout, and logger.
// The logger must not be nil.
func newTCPTransport(socket net.Conn, timeout time.Duration, logger *slog.Logger) *tcpTransport {
	return &tcpTransport{
		socket:  socket,
		timeout: timeout,
		logger:  logger,
	}
}

// Closes the underlying tcp socket.
func (tt *tcpTransport) Close() error {
	return tt.socket.Close()
}

// Runs a request across the socket and returns a response.
func (tt *tcpTransport) ExecuteRequest(req *pdu) (*pdu, error) {
	err := tt.socket.SetDeadline(time.Now().Add(tt.timeout))
	if err != nil {
		return nil, err
	}

	tt.lastTxnId++

	_, err = tt.socket.Write(tt.assembleMBAPFrame(tt.lastTxnId, req))
	if err != nil {
		return nil, err
	}

	return tt.readResponse()
}

// Reads a request from the socket.
func (tt *tcpTransport) ReadRequest() (*pdu, error) {
	err := tt.socket.SetDeadline(time.Now().Add(tt.timeout))
	if err != nil {
		return nil, err
	}

	req, txnId, err := tt.readMBAPFrame()
	if err != nil {
		return nil, err
	}

	tt.lastTxnId = txnId

	return req, nil
}

// Writes a response to the socket.
func (tt *tcpTransport) WriteResponse(res *pdu) error {
	_, err := tt.socket.Write(tt.assembleMBAPFrame(tt.lastTxnId, res))
	return err
}

// Reads as many MBAP+modbus frames as necessary until either the response
// matching tt.lastTxnId is received or an error occurs.
func (tt *tcpTransport) readResponse() (*pdu, error) {
	for {
		res, _, err := tt.readMBAPFrame()

		if err == ErrUnknownProtocolId {
			continue
		}

		if err != nil {
			return nil, err
		}

		return res, nil
	}
}

// Reads an entire frame (MBAP header + modbus PDU) from the socket.
func (tt *tcpTransport) readMBAPFrame() (*pdu, uint16, error) {
	rxbuf := make([]byte, mbapHeaderLength)
	_, err := io.ReadFull(tt.socket, rxbuf)
	if err != nil {
		return nil, 0, err
	}

	txnId := bytesToUint16(BIG_ENDIAN, rxbuf[0:2])
	protocolId := bytesToUint16(BIG_ENDIAN, rxbuf[2:4])
	unitId := rxbuf[6]

	bytesNeeded := int(bytesToUint16(BIG_ENDIAN, rxbuf[4:6]))

	if bytesNeeded+mbapHeaderLength > maxTCPFrameLength {
		return nil, 0, ErrProtocolError
	}

	if bytesNeeded <= 0 {
		return nil, 0, ErrProtocolError
	}

	rxbuf = make([]byte, bytesNeeded)
	_, err = io.ReadFull(tt.socket, rxbuf)
	if err != nil {
		return nil, 0, err
	}

	if protocolId != 0x0000 {
		tt.logger.Warn("received unexpected protocol id", "protocolId", fmt.Sprintf("%04X", protocolId))
		return nil, 0, ErrUnknownProtocolId
	}

	p := &pdu{
		unitId:       unitId,
		functionCode: rxbuf[0],
		payload:      rxbuf[1:],
	}

	return p, txnId, nil
}

// Turns a PDU into an MBAP frame (MBAP header + PDU) and returns it as bytes.
func (tt *tcpTransport) assembleMBAPFrame(txnId uint16, p *pdu) []byte {
	payload := uint16ToBytes(BIG_ENDIAN, txnId)
	payload = append(payload, 0x00, 0x00)
	payload = append(payload, uint16ToBytes(BIG_ENDIAN, uint16(2+len(p.payload)))...)
	payload = append(payload, p.unitId)
	payload = append(payload, p.functionCode)
	payload = append(payload, p.payload...)

	return payload
}
