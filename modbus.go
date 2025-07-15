package modbus

import (
	"fmt"
)

type transportType uint8

const (
	modbusRTU        transportType = 1
	modbusRTUOverTCP transportType = 2
	modbusRTUOverUDP transportType = 3
	modbusTCP        transportType = 4
	modbusTCPOverTLS transportType = 5
	modbusTCPOverUDP transportType = 6
)

type transport interface {
	Close() error
	ExecuteRequest(*pdu) (*pdu, error)
	ReadRequest() (*pdu, error)
	WriteResponse(*pdu) error
}

type pdu struct {
	unitId       uint8
	functionCode uint8
	payload      []byte
}

type Error string

func (me Error) Error() string {
	return string(me)
}

const (
	// coils
	fcReadCoils          uint8 = 0x01
	fcWriteSingleCoil    uint8 = 0x05
	fcWriteMultipleCoils uint8 = 0x0f

	// discrete inputs
	fcReadDiscreteInputs uint8 = 0x02

	// 16-bit input/holding registers
	fcReadHoldingRegisters       uint8 = 0x03
	fcReadInputRegisters         uint8 = 0x04
	fcWriteSingleRegister        uint8 = 0x06
	fcWriteMultipleRegisters     uint8 = 0x10
	fcMaskWriteRegister          uint8 = 0x16
	fcReadWriteMultipleRegisters uint8 = 0x17
	fcReadFifoQueue              uint8 = 0x18

	// exception codes
	exIllegalFunction         uint8 = 0x01
	exIllegalDataAddress      uint8 = 0x02
	exIllegalDataValue        uint8 = 0x03
	exServerDeviceFailure     uint8 = 0x04
	exAcknowledge             uint8 = 0x05
	exServerDeviceBusy        uint8 = 0x06
	exMemoryParityError       uint8 = 0x08
	exGWPathUnavailable       uint8 = 0x0a
	exGWTargetFailedToRespond uint8 = 0x0b

	// errors
	ErrConfigurationError      Error = "configuration error"
	ErrRequestTimedOut         Error = "request timed out"
	ErrIllegalFunction         Error = "illegal function"
	ErrIllegalDataAddress      Error = "illegal data address"
	ErrIllegalDataValue        Error = "illegal data value"
	ErrServerDeviceFailure     Error = "server device failure"
	ErrAcknowledge             Error = "request acknowledged"
	ErrServerDeviceBusy        Error = "server device busy"
	ErrMemoryParityError       Error = "memory parity error"
	ErrGWPathUnavailable       Error = "gateway path unavailable"
	ErrGWTargetFailedToRespond Error = "gateway target device failed to respond"
	ErrBadCRC                  Error = "bad crc"
	ErrShortFrame              Error = "short frame"
	ErrProtocolError           Error = "protocol error"
	ErrBadUnitId               Error = "bad unit id"
	ErrBadTransactionId        Error = "bad transaction id"
	ErrUnknownProtocolId       Error = "unknown protocol identifier"
	ErrUnexpectedParameters    Error = "unexpected parameters"
)

// mapExceptionCodeToError turns a modbus exception code into a higher level Error object.
func mapExceptionCodeToError(exceptionCode uint8) error {
	switch exceptionCode {
	case exIllegalFunction:
		return ErrIllegalFunction
	case exIllegalDataAddress:
		return ErrIllegalDataAddress
	case exIllegalDataValue:
		return ErrIllegalDataValue
	case exServerDeviceFailure:
		return ErrServerDeviceFailure
	case exAcknowledge:
		return ErrAcknowledge
	case exMemoryParityError:
		return ErrMemoryParityError
	case exServerDeviceBusy:
		return ErrServerDeviceBusy
	case exGWPathUnavailable:
		return ErrGWPathUnavailable
	case exGWTargetFailedToRespond:
		return ErrGWTargetFailedToRespond
	default:
		return fmt.Errorf("unknown exception code (%v)", exceptionCode)
	}
}

func mapErrorToExceptionCode(err error) uint8 {
	switch err {
	case ErrIllegalFunction:
		return exIllegalFunction
	case ErrIllegalDataAddress:
		return exIllegalDataAddress
	case ErrIllegalDataValue:
		return exIllegalDataValue
	case ErrServerDeviceFailure:
		return exServerDeviceFailure
	case ErrAcknowledge:
		return exAcknowledge
	case ErrMemoryParityError:
		return exMemoryParityError
	case ErrServerDeviceBusy:
		return exServerDeviceBusy
	case ErrGWPathUnavailable:
		return exGWPathUnavailable
	case ErrGWTargetFailedToRespond:
		return exGWTargetFailedToRespond
	default:
		return exServerDeviceFailure
	}
}
