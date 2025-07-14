package modbus

import (
	"fmt"
	"io"
	"log/slog"
	"time"
)

const (
	maxRTUFrameLength int = 256
)

type rtuTransport struct {
	logger       *slog.Logger
	link         rtuLink
	timeout      time.Duration
	lastActivity time.Time
	t35          time.Duration
	t1           time.Duration
}

type rtuLink interface {
	Close() error
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	SetDeadline(time.Time) error
}

// Returns a new RTU transport.
func newRTUTransport(link rtuLink, speed uint, timeout time.Duration, logger *slog.Logger) (rt *rtuTransport) {
	rt = &rtuTransport{
		logger:  logger.With("modbus", "RTU transport"),
		link:    link,
		timeout: timeout,
		t1:      serialCharTime(speed),
	}

	if speed >= 19200 {
		rt.t35 = 1750 * time.Microsecond
	} else {
		rt.t35 = (serialCharTime(speed) * 35) / 10
	}

	return
}

// Closes the rtu link.
func (rt *rtuTransport) Close() (err error) {
	err = rt.link.Close()

	return
}

// Runs a request across the rtu link and returns a response.
func (rt *rtuTransport) ExecuteRequest(req *pdu) (res *pdu, err error) {
	var ts time.Time
	var t time.Duration
	var n int

	err = rt.link.SetDeadline(time.Now().Add(rt.timeout))
	if err != nil {
		return
	}

	t = time.Since(rt.lastActivity.Add(rt.t35))
	if t < 0 {
		time.Sleep(t * (-1))
	}

	ts = time.Now()

	n, err = rt.link.Write(rt.assembleRTUFrame(req))
	if err != nil {
		return
	}

	rt.lastActivity = ts.Add(time.Duration(n) * rt.t1)

	time.Sleep(time.Until(rt.lastActivity.Add(rt.t35)))

	res, err = rt.readRTUFrame()

	if err == ErrBadCRC || err == ErrProtocolError || err == ErrShortFrame {
		time.Sleep(time.Duration(maxRTUFrameLength) * rt.t1)
		discard(rt.link)
	}

	if err != ErrRequestTimedOut {
		rt.lastActivity = time.Now()
	}

	return
}

// Reads a request from the rtu link.
func (rt *rtuTransport) ReadRequest() (req *pdu, err error) {
	err = fmt.Errorf("unimplemented")

	return
}

// Writes a response to the rtu link.
func (rt *rtuTransport) WriteResponse(res *pdu) (err error) {
	var n int

	n, err = rt.link.Write(rt.assembleRTUFrame(res))
	if err != nil {
		return
	}

	rt.lastActivity = time.Now().Add(rt.t1 * time.Duration(n))

	return
}

// Waits for, reads and decodes a frame from the rtu link.
func (rt *rtuTransport) readRTUFrame() (res *pdu, err error) {
	var rxbuf []byte
	var byteCount int
	var bytesNeeded int
	var crc crc

	rxbuf = make([]byte, maxRTUFrameLength)

	byteCount, err = io.ReadFull(rt.link, rxbuf[0:3])
	if (byteCount > 0 || err == nil) && byteCount != 3 {
		err = ErrShortFrame
		return
	}
	if err != nil && err != io.ErrUnexpectedEOF {
		return
	}

	bytesNeeded, err = expectedResponseLength(uint8(rxbuf[1]), uint8(rxbuf[2]))
	if err != nil {
		return
	}

	bytesNeeded += 2

	if byteCount+bytesNeeded > maxRTUFrameLength {
		err = ErrProtocolError
		return
	}

	byteCount, err = io.ReadFull(rt.link, rxbuf[3:3+bytesNeeded])
	if err != nil && err != io.ErrUnexpectedEOF {
		return
	}
	if byteCount != bytesNeeded {
		rt.logger.Warn("short response", "expected", bytesNeeded, "got", byteCount)
		err = ErrShortFrame
		return
	}

	crc.init()
	crc.add(rxbuf[0 : 3+bytesNeeded-2])

	if !crc.isEqual(rxbuf[3+bytesNeeded-2], rxbuf[3+bytesNeeded-1]) {
		err = ErrBadCRC
		return
	}

	res = &pdu{
		unitId:       rxbuf[0],
		functionCode: rxbuf[1],
		payload:      rxbuf[2 : 3+bytesNeeded-2],
	}

	return
}

// Turns a PDU object into bytes.
func (rt *rtuTransport) assembleRTUFrame(p *pdu) (adu []byte) {
	var crc crc

	adu = append(adu, p.unitId)
	adu = append(adu, p.functionCode)
	adu = append(adu, p.payload...)

	crc.init()
	crc.add(adu)

	adu = append(adu, crc.value()...)

	return
}

// Computes the expected length of a modbus RTU response.
func expectedResponseLength(responseCode uint8, responseLength uint8) (byteCount int, err error) {
	switch responseCode {
	case fcReadHoldingRegisters,
		fcReadInputRegisters,
		fcReadCoils,
		fcReadDiscreteInputs:
		byteCount = int(responseLength)
	case fcWriteSingleRegister,
		fcWriteMultipleRegisters,
		fcWriteSingleCoil,
		fcWriteMultipleCoils:
		byteCount = 3
	case fcMaskWriteRegister:
		byteCount = 5
	case fcReadHoldingRegisters | 0x80,
		fcReadInputRegisters | 0x80,
		fcReadCoils | 0x80,
		fcReadDiscreteInputs | 0x80,
		fcWriteSingleRegister | 0x80,
		fcWriteMultipleRegisters | 0x80,
		fcWriteSingleCoil | 0x80,
		fcWriteMultipleCoils | 0x80,
		fcMaskWriteRegister | 0x80:
		byteCount = 0
	default:
		err = ErrProtocolError
	}

	return
}

// Discards the contents of the link's rx buffer, eating up to 1kB of data.
// Note that on a serial line, this call may block for up to serialConf.Timeout
// i.e. 10ms.
func discard(link rtuLink) {
	var rxbuf = make([]byte, 1024)

	_ = link.SetDeadline(time.Now().Add(500 * time.Microsecond))
	_, _ = io.ReadFull(link, rxbuf)
}

// Returns how long it takes to send 1 byte on a serial line at the
// specified baud rate.
func serialCharTime(rate_bps uint) (ct time.Duration) {
	ct = (11) * time.Second / time.Duration(rate_bps)

	return
}
