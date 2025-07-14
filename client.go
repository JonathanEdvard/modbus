package modbus

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

type RegType uint8
type Endianness uint8
type WordOrder uint8

const (
	PARITY_NONE uint8 = 0
	PARITY_EVEN uint8 = 1
	PARITY_ODD  uint8 = 2

	HOLDING_REGISTER RegType = 0
	INPUT_REGISTER   RegType = 1

	// endianness of 16-bit registers
	BIG_ENDIAN    Endianness = 1
	LITTLE_ENDIAN Endianness = 2

	// word order of 32-bit registers
	HIGH_WORD_FIRST WordOrder = 1
	LOW_WORD_FIRST  WordOrder = 2
)

// Modbus client configuration object.
type ClientConfiguration struct {
	// URL sets the client mode and target location in the form
	// <mode>://<serial device or host:port> e.g. tcp://plc:502
	URL string
	// Speed sets the serial link speed (in bps, rtu only)
	Speed uint
	// DataBits sets the number of bits per serial character (rtu only)
	DataBits uint8
	// Parity sets the serial link parity mode (rtu only)
	Parity uint8
	// StopBits sets the number of serial stop bits (rtu only)
	StopBits uint8
	// Timeout sets the request timeout value
	Timeout time.Duration
	// TLSClientCert sets the client-side TLS key pair (tcp+tls only)
	TLSClientCert *tls.Certificate
	// TLSRootCAs sets the list of CA certificates used to authenticate
	// the server (tcp+tls only). Leaf (i.e. server) certificates can also
	// be used in case of self-signed certs, or if cert pinning is required.
	TLSRootCAs *x509.CertPool
}

// Modbus client object.
type ModbusClient struct {
	config        ClientConfiguration
	logger        *slog.Logger
	lock          sync.Mutex
	endianness    Endianness
	wordOrder     WordOrder
	transport     transport
	unitId        uint8
	transportType transportType
}

// NewClient creates, configures and returns a modbus client object.
func NewClient(conf *ClientConfiguration, logger *slog.Logger) (mc *ModbusClient, err error) {
	var clientType string
	var splitURL []string

	mc = &ModbusClient{
		config: *conf,
		logger: logger,
	}

	splitURL = strings.SplitN(mc.config.URL, "://", 2)
	if len(splitURL) != 2 {
		err = fmt.Errorf("invalid URL format: %w", ErrConfigurationError)
		return
	}
	clientType = splitURL[0]
	mc.config.URL = splitURL[1]

	switch clientType {
	case "rtu":
		err = mc.initRTU()
		if err != nil {
			return
		}
	case "rtuovertcp":
		err = mc.initRTUOverTCP()
		if err != nil {
			return
		}
	case "rtuoverudp":
		err = mc.initRTUOverUDP()
		if err != nil {
			return
		}
	case "tcp":
		err = mc.initTCP()
		if err != nil {
			return
		}
	case "tcp+tls":
		err = mc.initTCPOverTLS()
		if err != nil {
			return
		}
	case "udp":
		err = mc.initUDP()
		if err != nil {
			return
		}
	default:
		err = ErrConfigurationError
		return
	}

	mc.unitId = 1
	mc.endianness = BIG_ENDIAN
	mc.wordOrder = HIGH_WORD_FIRST

	return
}

func (mc *ModbusClient) initRTU() error {
	if mc.config.Speed == 0 {
		mc.config.Speed = 19200
	}
	if mc.config.DataBits == 0 {
		mc.config.DataBits = 8
	}
	if mc.config.StopBits == 0 {
		if mc.config.Parity == PARITY_NONE {
			mc.config.StopBits = 2
		} else {
			mc.config.StopBits = 1
		}
	}
	if mc.config.Timeout == 0 {
		mc.config.Timeout = 300 * time.Millisecond
	}
	mc.transportType = modbusRTU
	return nil
}

func (mc *ModbusClient) initRTUOverTCP() error {
	if mc.config.Speed == 0 {
		mc.config.Speed = 19200
	}
	if mc.config.Timeout == 0 {
		mc.config.Timeout = 1 * time.Second
	}
	mc.transportType = modbusRTUOverTCP
	return nil
}

func (mc *ModbusClient) initRTUOverUDP() error {
	if mc.config.Speed == 0 {
		mc.config.Speed = 19200
	}
	if mc.config.Timeout == 0 {
		mc.config.Timeout = 1 * time.Second
	}
	mc.transportType = modbusRTUOverUDP
	return nil
}

func (mc *ModbusClient) initTCP() error {
	if mc.config.Timeout == 0 {
		mc.config.Timeout = 1 * time.Second
	}
	mc.transportType = modbusTCP
	return nil
}

func (mc *ModbusClient) initTCPOverTLS() error {
	if mc.config.Timeout == 0 {
		mc.config.Timeout = 1 * time.Second
	}
	if mc.config.TLSClientCert == nil {
		mc.logger.Error("missing client certificate")
		return ErrConfigurationError
	}
	if mc.config.TLSRootCAs == nil {
		mc.logger.Error("missing CA/server certificate")
		return ErrConfigurationError
	}
	mc.transportType = modbusTCPOverTLS
	return nil
}

func (mc *ModbusClient) initUDP() error {
	if mc.config.Timeout == 0 {
		mc.config.Timeout = 1 * time.Second
	}
	mc.transportType = modbusTCPOverUDP
	return nil
}

// Opens the underlying transport (network socket or serial line).
func (mc *ModbusClient) Open() (err error) {
	var spw *serialPortWrapper
	var sock net.Conn

	mc.lock.Lock()
	defer mc.lock.Unlock()

	switch mc.transportType {
	case modbusRTU:
		spw = newSerialPortWrapper(&serialPortConfig{
			Device:   mc.config.URL,
			Speed:    mc.config.Speed,
			DataBits: mc.config.DataBits,
			Parity:   mc.config.Parity,
			StopBits: mc.config.StopBits,
		})

		err = spw.Open()
		if err != nil {
			return
		}

		discard(spw)

		mc.transport = newRTUTransport(
			spw, mc.config.Speed, mc.config.Timeout, mc.logger)

	case modbusRTUOverTCP:
		sock, err = net.DialTimeout("tcp", mc.config.URL, 5*time.Second)
		if err != nil {
			return
		}

		discard(sock)

		mc.transport = newRTUTransport(
			sock, mc.config.Speed, mc.config.Timeout, mc.logger)

	case modbusRTUOverUDP:
		sock, err = net.DialTimeout("udp", mc.config.URL, 5*time.Second)
		if err != nil {
			return
		}

		mc.transport = newRTUTransport(
			newUDPSockWrapper(sock),
			mc.config.Speed, mc.config.Timeout, mc.logger)

	case modbusTCP:
		sock, err = net.DialTimeout("tcp", mc.config.URL, 5*time.Second)
		if err != nil {
			return
		}

		mc.transport = newTCPTransport(sock, mc.config.Timeout, mc.logger)

	case modbusTCPOverTLS:
		sock, err = tls.DialWithDialer(
			&net.Dialer{
				Deadline: time.Now().Add(15 * time.Second),
			}, "tcp", mc.config.URL,
			&tls.Config{
				Certificates: []tls.Certificate{
					*mc.config.TLSClientCert,
				},
				RootCAs:    mc.config.TLSRootCAs,
				MinVersion: tls.VersionTLS12,
			})
		if err != nil {
			return
		}

		err = sock.(*tls.Conn).Handshake()
		if err != nil {
			sock.Close()
			return
		}

		mc.transport = newTCPTransport(
			newTLSSockWrapper(sock), mc.config.Timeout, mc.logger)

	case modbusTCPOverUDP:
		sock, err = net.DialTimeout("udp", mc.config.URL, 5*time.Second)
		if err != nil {
			return
		}

		mc.transport = newTCPTransport(
			newUDPSockWrapper(sock), mc.config.Timeout, mc.logger)

	default:
		err = ErrConfigurationError
	}

	if errors.Is(err, os.ErrDeadlineExceeded) {
		err = ErrRequestTimedOut
	}

	return
}

// Closes the underlying transport.
func (mc *ModbusClient) Close() (err error) {
	mc.lock.Lock()
	defer mc.lock.Unlock()

	if mc.transport != nil {
		err = mc.transport.Close()
	}

	return
}

// Sets the unit id of subsequent requests.
func (mc *ModbusClient) SetUnitId(id uint8) (err error) {
	mc.lock.Lock()
	defer mc.lock.Unlock()

	mc.unitId = id

	return
}

// Sets the encoding (endianness and word ordering) of subsequent requests.
func (mc *ModbusClient) SetEncoding(endianness Endianness, wordOrder WordOrder) (err error) {
	mc.lock.Lock()
	defer mc.lock.Unlock()

	if endianness != BIG_ENDIAN && endianness != LITTLE_ENDIAN {
		mc.logger.Error("unknown endianness", "value", endianness)
		err = ErrUnexpectedParameters
		return
	}

	if wordOrder != HIGH_WORD_FIRST && wordOrder != LOW_WORD_FIRST {
		mc.logger.Error("unknown word order value", "wordOrder", wordOrder)
		err = ErrUnexpectedParameters
		return
	}

	mc.endianness = endianness
	mc.wordOrder = wordOrder

	return
}

// Reads multiple coils (function code 01).
func (mc *ModbusClient) ReadCoils(addr uint16, quantity uint16) (values []bool, err error) {
	values, err = mc.readBools(addr, quantity, false)

	return
}

// Reads a single coil (function code 01).
func (mc *ModbusClient) ReadCoil(addr uint16) (value bool, err error) {
	var values []bool

	values, err = mc.readBools(addr, 1, false)
	if err == nil {
		value = values[0]
	}

	return
}

// Reads multiple discrete inputs (function code 02).
func (mc *ModbusClient) ReadDiscreteInputs(addr uint16, quantity uint16) (values []bool, err error) {
	values, err = mc.readBools(addr, quantity, true)

	return
}

// Reads a single discrete input (function code 02).
func (mc *ModbusClient) ReadDiscreteInput(addr uint16) (value bool, err error) {
	var values []bool

	values, err = mc.readBools(addr, 1, true)
	if err == nil {
		value = values[0]
	}

	return
}

// Reads multiple 16-bit registers (function code 03 or 04).
func (mc *ModbusClient) ReadRegisters(addr uint16, quantity uint16, regType RegType) (values []uint16, err error) {
	var mbPayload []byte

	mbPayload, err = mc.readRegisters(addr, quantity, regType)
	if err != nil {
		return
	}

	values = bytesToUint16s(mc.endianness, mbPayload)

	return
}

// Reads a single 16-bit register (function code 03 or 04).
func (mc *ModbusClient) ReadRegister(addr uint16, regType RegType) (value uint16, err error) {
	var values []uint16

	values, err = mc.ReadRegisters(addr, 1, regType)
	if err == nil {
		value = values[0]
	}

	return
}

// Reads multiple 32-bit registers.
func (mc *ModbusClient) ReadUint32s(addr uint16, quantity uint16, regType RegType) (values []uint32, err error) {
	var mbPayload []byte

	mbPayload, err = mc.readRegisters(addr, quantity*2, regType)
	if err != nil {
		return
	}

	values = bytesToUint32s(mc.endianness, mc.wordOrder, mbPayload)

	return
}

// Reads a single 32-bit register.
func (mc *ModbusClient) ReadUint32(addr uint16, regType RegType) (value uint32, err error) {
	var values []uint32

	values, err = mc.ReadUint32s(addr, 1, regType)
	if err == nil {
		value = values[0]
	}

	return
}

// Reads multiple 32-bit float registers.
func (mc *ModbusClient) ReadFloat32s(addr uint16, quantity uint16, regType RegType) (values []float32, err error) {
	var mbPayload []byte

	mbPayload, err = mc.readRegisters(addr, quantity*2, regType)
	if err != nil {
		return
	}

	values = bytesToFloat32s(mc.endianness, mc.wordOrder, mbPayload)

	return
}

// Reads a single 32-bit float register.
func (mc *ModbusClient) ReadFloat32(addr uint16, regType RegType) (value float32, err error) {
	var values []float32

	values, err = mc.ReadFloat32s(addr, 1, regType)
	if err == nil {
		value = values[0]
	}

	return
}

// Reads multiple 64-bit registers.
func (mc *ModbusClient) ReadUint64s(addr uint16, quantity uint16, regType RegType) (values []uint64, err error) {
	var mbPayload []byte

	mbPayload, err = mc.readRegisters(addr, quantity*4, regType)
	if err != nil {
		return
	}

	values = bytesToUint64s(mc.endianness, mc.wordOrder, mbPayload)

	return
}

// Reads a single 64-bit register.
func (mc *ModbusClient) ReadUint64(addr uint16, regType RegType) (value uint64, err error) {
	var values []uint64

	values, err = mc.ReadUint64s(addr, 1, regType)
	if err == nil {
		value = values[0]
	}

	return
}

// Reads multiple 64-bit float registers.
func (mc *ModbusClient) ReadFloat64s(addr uint16, quantity uint16, regType RegType) (values []float64, err error) {
	var mbPayload []byte

	mbPayload, err = mc.readRegisters(addr, quantity*4, regType)
	if err != nil {
		return
	}

	values = bytesToFloat64s(mc.endianness, mc.wordOrder, mbPayload)

	return
}

// Reads a single 64-bit float register.
func (mc *ModbusClient) ReadFloat64(addr uint16, regType RegType) (value float64, err error) {
	var values []float64

	values, err = mc.ReadFloat64s(addr, 1, regType)
	if err == nil {
		value = values[0]
	}

	return
}

// Reads one or multiple 16-bit registers (function code 03 or 04) as bytes.
// A per-register byteswap is performed if endianness is set to LITTLE_ENDIAN.
func (mc *ModbusClient) ReadBytes(addr uint16, quantity uint16, regType RegType) (values []byte, err error) {
	values, err = mc.readBytes(addr, quantity, regType, true)

	return
}

// Reads one or multiple 16-bit registers (function code 03 or 04) as bytes.
// No byte or word reordering is performed: bytes are returned exactly as they come
// off the wire, allowing the caller to handle encoding/endianness/word order manually.
func (mc *ModbusClient) ReadRawBytes(addr uint16, quantity uint16, regType RegType) (values []byte, err error) {
	values, err = mc.readBytes(addr, quantity, regType, false)

	return
}

// Writes a single coil (function code 05)
func (mc *ModbusClient) WriteCoil(addr uint16, value bool) (err error) {
	var req *pdu
	var res *pdu

	mc.lock.Lock()
	defer mc.lock.Unlock()

	req = &pdu{
		unitId:       mc.unitId,
		functionCode: fcWriteSingleCoil,
	}

	req.payload = uint16ToBytes(BIG_ENDIAN, addr)
	if value {
		req.payload = append(req.payload, 0xff, 0x00)
	} else {
		req.payload = append(req.payload, 0x00, 0x00)
	}

	res, err = mc.executeRequest(req)
	if err != nil {
		return
	}

	switch {
	case res.functionCode == req.functionCode:
		if len(res.payload) != 4 ||
			bytesToUint16(BIG_ENDIAN, res.payload[0:2]) != addr ||
			(value && res.payload[2] != 0xff) ||
			res.payload[3] != 0x00 {
			err = ErrProtocolError
			return
		}

	case res.functionCode == (req.functionCode | 0x80):
		if len(res.payload) != 1 {
			err = ErrProtocolError
			return
		}

		err = mapExceptionCodeToError(res.payload[0])

	default:
		err = ErrProtocolError
		mc.logger.Warn(fmt.Sprintf("unexpected response code (%v)", res.functionCode))
	}

	return
}

// Writes multiple coils (function code 15)
func (mc *ModbusClient) WriteCoils(addr uint16, values []bool) (err error) {
	var req *pdu
	var res *pdu
	var quantity uint16
	var encodedValues []byte

	mc.lock.Lock()
	defer mc.lock.Unlock()

	quantity = uint16(len(values))
	if quantity == 0 {
		err = ErrUnexpectedParameters
		mc.logger.Error("quantity of coils is 0")
		return
	}

	if quantity > 0x7b0 {
		err = ErrUnexpectedParameters
		mc.logger.Error("quantity of coils exceeds 1968")
		return
	}

	if uint32(addr)+uint32(quantity)-1 > 0xffff {
		err = ErrUnexpectedParameters
		mc.logger.Error("end coil address is past 0xffff")
		return
	}

	encodedValues = encodeBools(values)

	req = &pdu{
		unitId:       mc.unitId,
		functionCode: fcWriteMultipleCoils,
	}

	req.payload = uint16ToBytes(BIG_ENDIAN, addr)
	req.payload = append(req.payload, uint16ToBytes(BIG_ENDIAN, quantity)...)
	req.payload = append(req.payload, byte(len(encodedValues)))
	req.payload = append(req.payload, encodedValues...)

	res, err = mc.executeRequest(req)
	if err != nil {
		return
	}

	switch {
	case res.functionCode == req.functionCode:
		if len(res.payload) != 4 ||
			bytesToUint16(BIG_ENDIAN, res.payload[0:2]) != addr ||
			bytesToUint16(BIG_ENDIAN, res.payload[2:4]) != quantity {
			err = ErrProtocolError
			return
		}

	case res.functionCode == (req.functionCode | 0x80):
		if len(res.payload) != 1 {
			err = ErrProtocolError
			return
		}

		err = mapExceptionCodeToError(res.payload[0])

	default:
		err = ErrProtocolError
		mc.logger.Warn(fmt.Sprintf("unexpected response code (%v)", res.functionCode))
	}

	return
}

// Writes a single 16-bit register (function code 06).
func (mc *ModbusClient) WriteRegister(addr uint16, value uint16) (err error) {
	var req *pdu
	var res *pdu

	mc.lock.Lock()
	defer mc.lock.Unlock()

	req = &pdu{
		unitId:       mc.unitId,
		functionCode: fcWriteSingleRegister,
	}

	req.payload = uint16ToBytes(BIG_ENDIAN, addr)
	req.payload = append(req.payload, uint16ToBytes(mc.endianness, value)...)

	res, err = mc.executeRequest(req)
	if err != nil {
		return
	}

	switch {
	case res.functionCode == req.functionCode:
		if len(res.payload) != 4 ||
			bytesToUint16(BIG_ENDIAN, res.payload[0:2]) != addr ||
			bytesToUint16(mc.endianness, res.payload[2:4]) != value {
			err = ErrProtocolError
			return
		}

	case res.functionCode == (req.functionCode | 0x80):
		if len(res.payload) != 1 {
			err = ErrProtocolError
			return
		}

		err = mapExceptionCodeToError(res.payload[0])

	default:
		err = ErrProtocolError
		mc.logger.Warn(fmt.Sprintf("unexpected response code (%v)", res.functionCode))
	}

	return
}

// Writes multiple 16-bit registers (function code 16).
func (mc *ModbusClient) WriteRegisters(addr uint16, values []uint16) (err error) {
	var payload []byte

	for _, value := range values {
		payload = append(payload, uint16ToBytes(mc.endianness, value)...)
	}

	err = mc.writeRegisters(addr, payload)

	return
}

// Writes multiple 32-bit registers.
func (mc *ModbusClient) WriteUint32s(addr uint16, values []uint32) (err error) {
	var payload []byte

	for _, value := range values {
		payload = append(payload, uint32ToBytes(mc.endianness, mc.wordOrder, value)...)
	}

	err = mc.writeRegisters(addr, payload)

	return
}

// Writes a single 32-bit register.
func (mc *ModbusClient) WriteUint32(addr uint16, value uint32) (err error) {
	err = mc.writeRegisters(addr, uint32ToBytes(mc.endianness, mc.wordOrder, value))

	return
}

// Writes multiple 32-bit float registers.
func (mc *ModbusClient) WriteFloat32s(addr uint16, values []float32) (err error) {
	var payload []byte

	for _, value := range values {
		payload = append(payload, float32ToBytes(mc.endianness, mc.wordOrder, value)...)
	}

	err = mc.writeRegisters(addr, payload)

	return
}

// Writes a single 32-bit float register.
func (mc *ModbusClient) WriteFloat32(addr uint16, value float32) (err error) {
	err = mc.writeRegisters(addr, float32ToBytes(mc.endianness, mc.wordOrder, value))

	return
}

// Writes multiple 64-bit registers.
func (mc *ModbusClient) WriteUint64s(addr uint16, values []uint64) (err error) {
	var payload []byte

	for _, value := range values {
		payload = append(payload, uint64ToBytes(mc.endianness, mc.wordOrder, value)...)
	}

	err = mc.writeRegisters(addr, payload)

	return
}

// Writes a single 64-bit register.
func (mc *ModbusClient) WriteUint64(addr uint16, value uint64) (err error) {
	err = mc.writeRegisters(addr, uint64ToBytes(mc.endianness, mc.wordOrder, value))

	return
}

// Writes multiple 64-bit float registers.
func (mc *ModbusClient) WriteFloat64s(addr uint16, values []float64) (err error) {
	var payload []byte

	for _, value := range values {
		payload = append(payload, float64ToBytes(mc.endianness, mc.wordOrder, value)...)
	}

	err = mc.writeRegisters(addr, payload)

	return
}

// Writes a single 64-bit float register.
func (mc *ModbusClient) WriteFloat64(addr uint16, value float64) (err error) {
	err = mc.writeRegisters(addr, float64ToBytes(mc.endianness, mc.wordOrder, value))

	return
}

// Writes the given slice of bytes to 16-bit registers starting at addr.
// A per-register byteswap is performed if endianness is set to LITTLE_ENDIAN.
// Odd byte quantities are padded with a null byte to fall on 16-bit register boundaries.
func (mc *ModbusClient) WriteBytes(addr uint16, values []byte) (err error) {
	err = mc.writeBytes(addr, values, true)

	return
}

// Writes the given slice of bytes to 16-bit registers starting at addr.
// No byte or word reordering is performed: bytes are pushed to the wire as-is,
// allowing the caller to handle encoding/endianness/word order manually.
// Odd byte quantities are padded with a null byte to fall on 16-bit register boundaries.
func (mc *ModbusClient) WriteRawBytes(addr uint16, values []byte) (err error) {
	err = mc.writeBytes(addr, values, false)

	return
}

/*** unexported methods ***/
// Reads one or multiple 16-bit registers (function code 03 or 04) as bytes.
func (mc *ModbusClient) readBytes(addr uint16, quantity uint16, regType RegType, observeEndianness bool) (values []byte, err error) {
	regCount := (quantity / 2) + (quantity % 2)

	values, err = mc.readRegisters(addr, regCount, regType)
	if err != nil {
		return
	}

	if observeEndianness && mc.endianness == LITTLE_ENDIAN {
		for i := 0; i < len(values); i += 2 {
			values[i], values[i+1] = values[i+1], values[i]
		}
	}

	if quantity%2 == 1 {
		values = values[0 : len(values)-1]
	}

	return
}

// Writes the given slice of bytes to 16-bit registers starting at addr.
func (mc *ModbusClient) writeBytes(addr uint16, values []byte, observeEndianness bool) (err error) {
	if len(values)%2 == 1 {
		values = append(values, 0x00)
	}

	if observeEndianness && mc.endianness == LITTLE_ENDIAN {
		for i := 0; i < len(values); i += 2 {
			values[i], values[i+1] = values[i+1], values[i]
		}
	}

	err = mc.writeRegisters(addr, values)

	return
}

// Reads and returns quantity booleans.
// Digital inputs are read if di is true, otherwise coils are read.
func (mc *ModbusClient) readBools(addr uint16, quantity uint16, di bool) (values []bool, err error) {
	var req *pdu
	var res *pdu
	var expectedLen int

	mc.lock.Lock()
	defer mc.lock.Unlock()

	if quantity == 0 {
		err = ErrUnexpectedParameters
		mc.logger.Error("quantity of coils/discrete inputs is 0")
		return
	}

	if quantity > 2000 {
		err = ErrUnexpectedParameters
		mc.logger.Error("quantity of coils/discrete inputs exceeds 2000")
		return
	}

	if uint32(addr)+uint32(quantity)-1 > 0xffff {
		err = ErrUnexpectedParameters
		mc.logger.Error("end coil/discrete input address is past 0xffff")
		return
	}

	req = &pdu{
		unitId: mc.unitId,
	}

	if di {
		req.functionCode = fcReadDiscreteInputs
	} else {
		req.functionCode = fcReadCoils
	}

	req.payload = uint16ToBytes(BIG_ENDIAN, addr)
	req.payload = append(req.payload, uint16ToBytes(BIG_ENDIAN, quantity)...)

	res, err = mc.executeRequest(req)
	if err != nil {
		return
	}

	switch {
	case res.functionCode == req.functionCode:
		expectedLen = 1
		expectedLen += int(quantity) / 8
		if quantity%8 != 0 {
			expectedLen++
		}

		if len(res.payload) != expectedLen {
			err = ErrProtocolError
			return
		}

		if int(res.payload[0])+1 != expectedLen {
			err = ErrProtocolError
			return
		}

		values = decodeBools(quantity, res.payload[1:])

	case res.functionCode == (req.functionCode | 0x80):
		if len(res.payload) != 1 {
			err = ErrProtocolError
			return
		}

		err = mapExceptionCodeToError(res.payload[0])

	default:
		err = ErrProtocolError
		mc.logger.Warn(fmt.Sprintf("unexpected response code (%v)", res.functionCode))
	}

	return
}

// Reads and returns quantity registers of type regType, as bytes.
func (mc *ModbusClient) readRegisters(addr uint16, quantity uint16, regType RegType) (bytes []byte, err error) {
	var req *pdu
	var res *pdu

	mc.lock.Lock()
	defer mc.lock.Unlock()

	req = &pdu{
		unitId: mc.unitId,
	}

	switch regType {
	case HOLDING_REGISTER:
		req.functionCode = fcReadHoldingRegisters
	case INPUT_REGISTER:
		req.functionCode = fcReadInputRegisters
	default:
		err = ErrUnexpectedParameters
		mc.logger.Error("unexpected register", "type", regType)
		return
	}

	if quantity == 0 {
		err = ErrUnexpectedParameters
		mc.logger.Error("quantity of registers is 0")
		return
	}

	if quantity > 125 {
		err = ErrUnexpectedParameters
		mc.logger.Error("quantity of registers exceeds 125")
		return
	}

	if uint32(addr)+uint32(quantity)-1 > 0xffff {
		err = ErrUnexpectedParameters
		mc.logger.Error("end register address is past 0xffff")
		return
	}

	req.payload = uint16ToBytes(BIG_ENDIAN, addr)
	req.payload = append(req.payload, uint16ToBytes(BIG_ENDIAN, quantity)...)

	res, err = mc.executeRequest(req)
	if err != nil {
		return
	}

	switch {
	case res.functionCode == req.functionCode:
		if len(res.payload) != 1+2*int(quantity) {
			err = ErrProtocolError
			return
		}

		if uint(res.payload[0]) != 2*uint(quantity) {
			err = ErrProtocolError
			return
		}

		bytes = res.payload[1:]

	case res.functionCode == (req.functionCode | 0x80):
		if len(res.payload) != 1 {
			err = ErrProtocolError
			return
		}

		err = mapExceptionCodeToError(res.payload[0])

	default:
		err = ErrProtocolError
		mc.logger.Warn(fmt.Sprintf("unexpected response code (%v)", res.functionCode))
	}

	return
}

// Writes multiple registers starting from base address addr.
// Register values are passed as bytes, each value being exactly 2 bytes.
func (mc *ModbusClient) writeRegisters(addr uint16, values []byte) (err error) {
	var req *pdu
	var res *pdu
	var payloadLength uint16
	var quantity uint16

	mc.lock.Lock()
	defer mc.lock.Unlock()

	payloadLength = uint16(len(values))
	quantity = payloadLength / 2

	if quantity == 0 {
		err = ErrUnexpectedParameters
		mc.logger.Error("quantity of registers is 0")
		return
	}

	if quantity > 123 {
		err = ErrUnexpectedParameters
		mc.logger.Error("quantity of registers exceeds 123")
		return
	}

	if uint32(addr)+uint32(quantity)-1 > 0xffff {
		err = ErrUnexpectedParameters
		mc.logger.Error("end register address is past 0xffff")
		return
	}

	req = &pdu{
		unitId:       mc.unitId,
		functionCode: fcWriteMultipleRegisters,
	}

	req.payload = uint16ToBytes(BIG_ENDIAN, addr)
	req.payload = append(req.payload, uint16ToBytes(BIG_ENDIAN, quantity)...)
	req.payload = append(req.payload, byte(payloadLength))
	req.payload = append(req.payload, values...)

	res, err = mc.executeRequest(req)
	if err != nil {
		return
	}

	switch {
	case res.functionCode == req.functionCode:
		if len(res.payload) != 4 ||
			bytesToUint16(BIG_ENDIAN, res.payload[0:2]) != addr ||
			bytesToUint16(BIG_ENDIAN, res.payload[2:4]) != quantity {
			err = ErrProtocolError
			return
		}

	case res.functionCode == (req.functionCode | 0x80):
		if len(res.payload) != 1 {
			err = ErrProtocolError
			return
		}

		err = mapExceptionCodeToError(res.payload[0])

	default:
		err = ErrProtocolError
		mc.logger.Warn(fmt.Sprintf("unexpected response code (%v)", res.functionCode))
	}

	return
}

func (mc *ModbusClient) executeRequest(req *pdu) (res *pdu, err error) {
	res, err = mc.transport.ExecuteRequest(req)
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			err = ErrRequestTimedOut
		}
		return
	}

	if (res.functionCode&0x80) == 0x00 && res.unitId != req.unitId {
		err = ErrBadUnitId
		return
	}
	if (res.functionCode&0x80) == 0x80 &&
		(res.unitId != req.unitId && res.unitId != 0xff) {
		err = ErrBadUnitId
		return
	}

	return
}
