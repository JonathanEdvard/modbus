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
		rt.t35 = 1750 * time.Microsecond
	} else {
		rt.t35 = (serialCharTime(speed) * 35) / 10
	}

	return rt
}

func (rt *rtuTransport) Close() error {
	return rt.link.Close()
}

func (rt *rtuTransport) ExecuteRequest(req *pdu) (*pdu, error) {

	err := rt.link.SetDeadline(time.Now().Add(rt.timeout))
	if err != nil {
		return nil, err
	}

	timeBeforeStart := time.Since(rt.lastActivity.Add(rt.t35))
	if timeBeforeStart < 0 {
		time.Sleep(timeBeforeStart * (-1))
	}

	timestampStart := time.Now()

	n, err := rt.link.Write(rt.assembleRTUFrame(req))
	if err != nil {
		return nil, err
	}

	rt.lastActivity = timestampStart.Add(time.Duration(n) * rt.t1)

	time.Sleep(time.Until(rt.lastActivity.Add(rt.t35)))

	res, err := rt.readRTUFrame()

	if err == ErrBadCRC || err == ErrProtocolError || err == ErrShortFrame {
		time.Sleep(time.Duration(maxRTUFrameLength) * rt.t1)
		discard(rt.link)
	}

	if err != ErrRequestTimedOut {
		rt.lastActivity = time.Now()
	}

	return res, err
}

func (rt *rtuTransport) ReadRequest() (*pdu, error) {
	return nil, fmt.Errorf("unimplemented")
}

func (rt *rtuTransport) WriteResponse(res *pdu) error {
	n, err := rt.link.Write(rt.assembleRTUFrame(res))
	if err != nil {
		return err
	}

	rt.lastActivity = time.Now().Add(rt.t1 * time.Duration(n))

	return nil
}

func (rt *rtuTransport) readRTUFrame() (*pdu, error) {
	rxbuf := make([]byte, maxRTUFrameLength)

	byteCount, err := io.ReadFull(rt.link, rxbuf[0:3])
	if (byteCount > 0 || err == nil) && byteCount != 3 {
		return nil, ErrShortFrame
	}
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}

	bytesExpected, err := expectedResponseLength(uint8(rxbuf[1]), uint8(rxbuf[2]))
	if err != nil {
		return nil, err
	}
	bytesExpected += 2 // 2 bytes for CRC

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

	crc := newCRC()
	crc.add(rxbuf[0 : 3+bytesExpected-2])

	if !crc.isEqual(rxbuf[3+bytesExpected-2], rxbuf[3+bytesExpected-1]) {
		return nil, ErrBadCRC
	}

	return &pdu{
		unitId:       rxbuf[0],
		functionCode: rxbuf[1],
		payload:      rxbuf[2 : 3+bytesExpected-2],
	}, nil
}

func (rt *rtuTransport) assembleRTUFrame(p *pdu) []byte {
	adu := []byte{p.unitId, p.functionCode}
	adu = append(adu, p.payload...)

	crc := newCRC()
	crc.add(adu)

	adu = append(adu, crc.value()...)

	return adu
}

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

func discard(link rtuLink) {
	rxbuf := make([]byte, 1024)

	_ = link.SetDeadline(time.Now().Add(500 * time.Microsecond))
	_, _ = io.ReadFull(link, rxbuf)
}

func serialCharTime(rate_bps uint) time.Duration {
	return 11 * time.Second / time.Duration(rate_bps)
}
