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
func newRTUTransport(link rtuLink, speed uint, timeout time.Duration, logger *slog.Logger) *rtuTransport {
	rt := &rtuTransport{
		link:    link,
		t1:      serialCharTime(speed),
		timeout: timeout,
		logger:  logger.With("modbus.transport", "rtu"),
	}

	if speed >= 19200 {
		// for baud rates equal to or greater than 19200 bauds, a fixed value of
		// 1750 uS is specified for t3.5.
		rt.t35 = 1750 * time.Microsecond
	} else {
		// for lower baud rates, the inter-frame delay should be 3.5 character times
		rt.t35 = (serialCharTime(speed) * 35) / 10
	}

	return rt
}

// Closes the rtu link.
func (rt *rtuTransport) Close() error {
	return rt.link.Close()
}

// Runs a request across the rtu link and returns a response.
func (rt *rtuTransport) ExecuteRequest(req *pdu) (*pdu, error) {

	// set an i/o deadline on the link
	err := rt.link.SetDeadline(time.Now().Add(rt.timeout))
	if err != nil {
		return nil, err
	}

	// if the line was active less than 3.5 char times ago,
	// let t3.5 expire before transmitting
	timeBeforeStart := time.Since(rt.lastActivity.Add(rt.t35))
	if timeBeforeStart < 0 {
		time.Sleep(timeBeforeStart * (-1))
	}

	timestampStart := time.Now()

	// build an RTU ADU out of the request object and
	// send the final ADU+CRC on the wire
	n, err := rt.link.Write(rt.assembleRTUFrame(req))
	if err != nil {
		return nil, err
	}

	// estimate how long the serial line was busy for.
	// note that on most platforms, Write() will be buffered and return
	// immediately rather than block until the buffer is drained
	rt.lastActivity = timestampStart.Add(time.Duration(n) * rt.t1)

	// observe inter-frame delays
	time.Sleep(time.Until(rt.lastActivity.Add(rt.t35)))

	// read the response back from the wire
	res, err := rt.readRTUFrame()

	if err == ErrBadCRC || err == ErrProtocolError || err == ErrShortFrame {
		// wait for and flush any data coming off the link to allow
		// devices to re-sync
		time.Sleep(time.Duration(maxRTUFrameLength) * rt.t1)
		discard(rt.link)
	}

	// mark the time if we heard anything back
	if err != ErrRequestTimedOut {
		rt.lastActivity = time.Now()
	}

	return res, err
}

// Reads a request from the rtu link.
func (rt *rtuTransport) ReadRequest() (*pdu, error) {
	// reading requests from RTU links is currently unsupported
	return nil, fmt.Errorf("unimplemented")
}

// Writes a response to the rtu link.
func (rt *rtuTransport) WriteResponse(res *pdu) error {
	// build an RTU ADU out of the request object and
	// send the final ADU+CRC on the wire
	n, err := rt.link.Write(rt.assembleRTUFrame(res))
	if err != nil {
		return err
	}

	rt.lastActivity = time.Now().Add(rt.t1 * time.Duration(n))

	return nil
}

// Waits for, reads and decodes a frame from the rtu link.
func (rt *rtuTransport) readRTUFrame() (*pdu, error) {
	rxbuf := make([]byte, maxRTUFrameLength)

	// read the serial ADU header: unit id (1 byte), function code (1 byte) and
	// PDU length/exception code (1 byte)
	byteCount, err := io.ReadFull(rt.link, rxbuf[0:3])
	if (byteCount > 0 || err == nil) && byteCount != 3 {
		return nil, ErrShortFrame
	}
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}

	// figure out how many further bytes to read
	bytesExpected, err := expectedResponseLength(uint8(rxbuf[1]), uint8(rxbuf[2]))
	if err != nil {
		return nil, err
	}

	// we need to read 2 additional bytes of CRC after the payload
	bytesExpected += 2

	// never read more than the max allowed frame length
	if byteCount+bytesExpected > maxRTUFrameLength {
		return nil, ErrProtocolError
	}

	byteCount, err = io.ReadFull(rt.link, rxbuf[3:3+bytesExpected])
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	if byteCount != bytesExpected {
		rt.logger.Warn("wrong byteCount", "expected", bytesExpected, "received", byteCount)
		return nil, ErrShortFrame
	}

	// compute the CRC on the entire frame, excluding the CRC
	crc := newCRC()
	crc.add(rxbuf[0 : 3+bytesExpected-2])

	// compare CRC values
	if !crc.isEqual(rxbuf[3+bytesExpected-2], rxbuf[3+bytesExpected-1]) {
		return nil, ErrBadCRC
	}

	return &pdu{
		unitId:       rxbuf[0],
		functionCode: rxbuf[1],
		// pass the byte count + trailing data as payload, withtout the CRC
		payload: rxbuf[2 : 3+bytesExpected-2],
	}, nil
}

// Turns a PDU object into bytes.
func (rt *rtuTransport) assembleRTUFrame(p *pdu) []byte {
	adu := []byte{p.unitId, p.functionCode}
	adu = append(adu, p.payload...)

	// run the ADU through the CRC generator
	crc := newCRC()
	crc.add(adu)

	// append the CRC to the ADU
	adu = append(adu, crc.value()...)

	return adu
}

// Computes the expected length of a modbus RTU response.
func expectedResponseLength(responseCode uint8, responseLength uint8) (int, error) {
	switch responseCode {
	case fcReadHoldingRegisters,
		fcReadInputRegisters,
		fcReadCoils,
		fcReadDiscreteInputs:
		return int(responseLength), nil
	case fcWriteSingleRegister,
		fcWriteMultipleRegisters,
		fcWriteSingleCoil,
		fcWriteMultipleCoils:
		return 3, nil
	case fcMaskWriteRegister:
		return 5, nil
	case fcReadHoldingRegisters | 0x80,
		fcReadInputRegisters | 0x80,
		fcReadCoils | 0x80,
		fcReadDiscreteInputs | 0x80,
		fcWriteSingleRegister | 0x80,
		fcWriteMultipleRegisters | 0x80,
		fcWriteSingleCoil | 0x80,
		fcWriteMultipleCoils | 0x80,
		fcMaskWriteRegister | 0x80:
		return 0, nil
	default:
		return 0, ErrProtocolError
	}
}

// Discards the contents of the link's rx buffer, eating up to 1kB of data.
// Note that on a serial line, this call may block for up to serialConf.Timeout
// i.e. 10ms.
func discard(link rtuLink) {
	rxbuf := make([]byte, 1024)

	_ = link.SetDeadline(time.Now().Add(500 * time.Microsecond))
	_, _ = io.ReadFull(link, rxbuf)
}

// Returns how long it takes to send 1 byte on a serial line at the
// specified baud rate.
func serialCharTime(rate_bps uint) time.Duration {
	// note: an RTU byte on the wire is:
	// - 1 start bit,
	// - 8 data bits,
	// - 1 parity or stop bit
	// - 1 stop bit
	return 11 * time.Second / time.Duration(rate_bps)
}
