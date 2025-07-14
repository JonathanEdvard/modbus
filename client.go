package modbus

import (
	"crypto/tls"
	"crypto/x509"
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
func NewClient(conf *ClientConfiguration, logger *slog.Logger) (*ModbusClient, error) {
	var clientType string
	var splitURL []string

	mc := &ModbusClient{
		config: *conf,
		logger: logger,
	}

	splitURL = strings.SplitN(mc.config.URL, "://", 2)
	if len(splitURL) == 2 {
		clientType = splitURL[0]
		mc.config.URL = splitURL[1]
	}

	switch clientType {
	case "rtu":
		// set useful defaults
		if mc.config.Speed == 0 {
			mc.config.Speed = 19200
		}

		// note: the "modbus over serial line v1.02" document specifies an
		// 11-bit character frame, with even parity and 1 stop bit as default,
		// and mandates the use of 2 stop bits when no parity is used.
		// This stack defaults to 8/N/2 as most devices seem to use no parity,
		// but giving 8/N/1, 8/E/1 and 8/O/1 a shot may help with serial
		// issues.
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

	case "rtuovertcp":
		if mc.config.Speed == 0 {
			mc.config.Speed = 19200
		}

		if mc.config.Timeout == 0 {
			mc.config.Timeout = 1 * time.Second
		}

		mc.transportType = modbusRTUOverTCP

	case "rtuoverudp":
		if mc.config.Speed == 0 {
			mc.config.Speed = 19200
		}

		if mc.config.Timeout == 0 {
			mc.config.Timeout = 1 * time.Second
		}

		mc.transportType = modbusRTUOverUDP

	case "tcp":
		if mc.config.Timeout == 0 {
			mc.config.Timeout = 1 * time.Second
		}

		mc.transportType = modbusTCP

	case "tcp+tls":
		if mc.config.Timeout == 0 {
			mc.config.Timeout = 1 * time.Second
		}

		// expect a client-side certificate for mutual auth as the
		// modbus/mpab protocol has no inherent auth facility.
		// (see requirements R-08 and R-19 of the MBAPS spec)
		if mc.config.TLSClientCert == nil {
			mc.logger.Error("missing client certificate")
			return nil, ErrConfigurationError
		}

		// expect a CertPool object containing at least 1 CA or
		// leaf certificate to validate the server-side cert
		if mc.config.TLSRootCAs == nil {
			mc.logger.Error("missing CA/server certificate")
			return nil, ErrConfigurationError
		}

		mc.transportType = modbusTCPOverTLS

	case "udp":
		if mc.config.Timeout == 0 {
			mc.config.Timeout = 1 * time.Second
		}

		mc.transportType = modbusTCPOverUDP

	default:
		if len(splitURL) != 2 {
			mc.logger.Error("missing client type in URL", "URL", mc.config.URL)
		} else {
			mc.logger.Error("unsupported type", "clientType", clientType)
		}
		return nil, ErrConfigurationError
	}

	mc.unitId = 1
	mc.endianness = BIG_ENDIAN
	mc.wordOrder = HIGH_WORD_FIRST

	return mc, nil
}

// Opens the underlying transport (network socket or serial line).
func (mc *ModbusClient) Open() error {
	var spw *serialPortWrapper

	mc.lock.Lock()
	defer mc.lock.Unlock()

	switch mc.transportType {
	case modbusRTU:
		// create a serial port wrapper object
		spw = newSerialPortWrapper(&serialPortConfig{
			Device:   mc.config.URL,
			Speed:    mc.config.Speed,
			DataBits: mc.config.DataBits,
			Parity:   mc.config.Parity,
			StopBits: mc.config.StopBits,
		})

		// open the serial device
		err := spw.Open()
		if err != nil {
			return err
		}

		// discard potentially stale serial data
		discard(spw)

		// create the RTU transport
		mc.transport = newRTUTransport(
			spw, mc.config.Speed, mc.config.Timeout, mc.logger)

	case modbusRTUOverTCP:
		// connect to the remote host
		sock, err := net.DialTimeout("tcp", mc.config.URL, 5*time.Second)
		if err != nil {
			return err
		}

		// discard potentially stale serial data
		discard(sock)

		// create the RTU transport
		mc.transport = newRTUTransport(sock, mc.config.Speed, mc.config.Timeout, mc.logger)

	case modbusRTUOverUDP:
		// open a socket to the remote host (note: no actual connection is
		// being made as UDP is connection-less)
		sock, err := net.DialTimeout("udp", mc.config.URL, 5*time.Second)
		if err != nil {
			return err
		}

		// create the RTU transport, wrapping the UDP socket in
		// an adapter to allow the transport to read the stream of
		// packets byte per byte
		mc.transport = newRTUTransport(
			newUDPSockWrapper(sock),
			mc.config.Speed, mc.config.Timeout, mc.logger)

	case modbusTCP:
		// connect to the remote host
		sock, err := net.DialTimeout("tcp", mc.config.URL, 5*time.Second)
		if err != nil {
			return err
		}

		// create the TCP transport
		mc.transport = newTCPTransport(sock, mc.config.Timeout, mc.logger)

	case modbusTCPOverTLS:
		// connect to the remote host with TLS
		var sock net.Conn
		sock, err := tls.DialWithDialer(
			&net.Dialer{
				Deadline: time.Now().Add(15 * time.Second),
			}, "tcp", mc.config.URL,
			&tls.Config{
				Certificates: []tls.Certificate{
					*mc.config.TLSClientCert,
				},
				RootCAs: mc.config.TLSRootCAs,
				// mandate TLS 1.2 or higher (see R-01 of the MBAPS spec)
				MinVersion: tls.VersionTLS12,
			})
		if err != nil {
			return err
		}

		// force the TLS handshake
		err = sock.(*tls.Conn).Handshake()
		if err != nil {
			sock.Close()
			return err
		}

		// create the TCP transport, wrapping the TLS socket in
		// an adapter to work around write timeouts corrupting internal
		// state (see https://pkg.go.dev/crypto/tls#Conn.SetWriteDeadline)
		mc.transport = newTCPTransport(
			newTLSSockWrapper(sock), mc.config.Timeout, mc.logger)

	case modbusTCPOverUDP:
		// open a socket to the remote host (note: no actual connection is
		// being made as UDP is connection-less)
		sock, err := net.DialTimeout("udp", mc.config.URL, 5*time.Second)
		if err != nil {
			return err
		}

		// create the TCP transport, wrapping the UDP socket in
		// an adapter to allow the transport to read the stream of
		// packets byte per byte
		mc.transport = newTCPTransport(
			newUDPSockWrapper(sock), mc.config.Timeout, mc.logger)

	default:
		// should never happen
		return ErrConfigurationError
	}

	return nil
}

// Closes the underlying transport.
func (mc *ModbusClient) Close() error {
	mc.lock.Lock()
	defer mc.lock.Unlock()

	if mc.transport != nil {
		return mc.transport.Close()
	}

	return nil
}

// Sets the unit id of subsequent requests.
func (mc *ModbusClient) SetUnitId(id uint8) error {
	mc.lock.Lock()
	defer mc.lock.Unlock()

	mc.unitId = id

	return nil
}

// Sets the encoding (endianness and word ordering) of subsequent requests.
func (mc *ModbusClient) SetEncoding(endianness Endianness, wordOrder WordOrder) error {
	mc.lock.Lock()
	defer mc.lock.Unlock()

	if endianness != BIG_ENDIAN && endianness != LITTLE_ENDIAN {
		mc.logger.Error("unknown endianness", "value", endianness)
		return ErrUnexpectedParameters
	}

	if wordOrder != HIGH_WORD_FIRST && wordOrder != LOW_WORD_FIRST {
		mc.logger.Error("unknown word order value", "wordOrder", wordOrder)
		return ErrUnexpectedParameters
	}

	mc.endianness = endianness
	mc.wordOrder = wordOrder

	return nil
}

// Reads multiple coils (function code 01).
func (mc *ModbusClient) ReadCoils(addr uint16, quantity uint16) ([]bool, error) {
	return mc.readBools(addr, quantity, false)
}

// Reads a single coil (function code 01).
func (mc *ModbusClient) ReadCoil(addr uint16) (bool, error) {
	values, err := mc.readBools(addr, 1, false)
	if err != nil {
		return false, err
	}
	return values[0], nil
}

// Reads multiple discrete inputs (function code 02).
func (mc *ModbusClient) ReadDiscreteInputs(addr uint16, quantity uint16) ([]bool, error) {
	return mc.readBools(addr, quantity, true)
}

// Reads a single discrete input (function code 02).
func (mc *ModbusClient) ReadDiscreteInput(addr uint16) (bool, error) {
	values, err := mc.readBools(addr, 1, true)
	if err != nil {
		return false, err
	}
	return values[0], nil
}

// Reads multiple 16-bit registers (function code 03 or 04).
func (mc *ModbusClient) ReadRegisters(addr uint16, quantity uint16, regType RegType) ([]uint16, error) {
	mbPayload, err := mc.readRegisters(addr, quantity, regType)
	if err != nil {
		return nil, err
	}

	return bytesToUint16s(mc.endianness, mbPayload), nil
}

// Reads a single 16-bit register (function code 03 or 04).
func (mc *ModbusClient) ReadRegister(addr uint16, regType RegType) (uint16, error) {
	values, err := mc.ReadRegisters(addr, 1, regType)
	if err != nil {
		return 0, err
	}
	return values[0], nil
}

// Reads multiple 32-bit registers.
func (mc *ModbusClient) ReadUint32s(addr uint16, quantity uint16, regType RegType) ([]uint32, error) {
	mbPayload, err := mc.readRegisters(addr, quantity*2, regType)
	if err != nil {
		return nil, err
	}

	return bytesToUint32s(mc.endianness, mc.wordOrder, mbPayload), nil
}

// Reads a single 32-bit register.
func (mc *ModbusClient) ReadUint32(addr uint16, regType RegType) (uint32, error) {
	values, err := mc.ReadUint32s(addr, 1, regType)
	if err != nil {
		return 0, err
	}
	return values[0], nil
}

// Reads multiple 32-bit float registers.
func (mc *ModbusClient) ReadFloat32s(addr uint16, quantity uint16, regType RegType) ([]float32, error) {
	mbPayload, err := mc.readRegisters(addr, quantity*2, regType)
	if err != nil {
		return nil, err
	}

	return bytesToFloat32s(mc.endianness, mc.wordOrder, mbPayload), nil
}

// Reads a single 32-bit float register.
func (mc *ModbusClient) ReadFloat32(addr uint16, regType RegType) (float32, error) {
	values, err := mc.ReadFloat32s(addr, 1, regType)
	if err != nil {
		return 0, err
	}
	return values[0], nil
}

// Reads multiple 64-bit registers.
func (mc *ModbusClient) ReadUint64s(addr uint16, quantity uint16, regType RegType) ([]uint64, error) {
	mbPayload, err := mc.readRegisters(addr, quantity*4, regType)
	if err != nil {
		return nil, err
	}

	return bytesToUint64s(mc.endianness, mc.wordOrder, mbPayload), nil
}

// Reads a single 64-bit register.
func (mc *ModbusClient) ReadUint64(addr uint16, regType RegType) (uint64, error) {
	values, err := mc.ReadUint64s(addr, 1, regType)
	if err != nil {
		return 0, err
	}
	return values[0], nil
}

// Reads multiple 64-bit float registers.
func (mc *ModbusClient) ReadFloat64s(addr uint16, quantity uint16, regType RegType) ([]float64, error) {
	mbPayload, err := mc.readRegisters(addr, quantity*4, regType)
	if err != nil {
		return nil, err
	}

	return bytesToFloat64s(mc.endianness, mc.wordOrder, mbPayload), nil
}

// Reads a single 64-bit float register.
func (mc *ModbusClient) ReadFloat64(addr uint16, regType RegType) (float64, error) {
	values, err := mc.ReadFloat64s(addr, 1, regType)
	if err != nil {
		return 0, err
	}
	return values[0], nil
}

// Reads one or multiple 16-bit registers (function code 03 or 04) as bytes.
// A per-register byteswap is performed if endianness is set to LITTLE_ENDIAN.
func (mc *ModbusClient) ReadBytes(addr uint16, quantity uint16, regType RegType) ([]byte, error) {
	return mc.readBytes(addr, quantity, regType, true)
}

// Reads one or multiple 16-bit registers (function code 03 or 04) as bytes.
// No byte or word reordering is performed: bytes are returned exactly as they come
// off the wire, allowing the caller to handle encoding/endianness/word order manually.
func (mc *ModbusClient) ReadRawBytes(addr uint16, quantity uint16, regType RegType) ([]byte, error) {
	return mc.readBytes(addr, quantity, regType, false)
}

// Writes a single coil (function code 05)
func (mc *ModbusClient) WriteCoil(addr uint16, value bool) error {
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

	res, err := mc.executeRequest(req)
	if err != nil {
		return err
	}

	switch {
	case res.functionCode == req.functionCode:
		if len(res.payload) != 4 ||
			bytesToUint16(BIG_ENDIAN, res.payload[0:2]) != addr ||
			(value && res.payload[2] != 0xff) ||
			res.payload[3] != 0x00 {
			return ErrProtocolError
		}

	case res.functionCode == (req.functionCode | 0x80):
		if len(res.payload) != 1 {
			return ErrProtocolError
		}

		return mapExceptionCodeToError(res.payload[0])

	default:
		mc.logger.Warn(fmt.Sprintf("unexpected response code (%v)", res.functionCode))
		return ErrProtocolError
	}

	return nil
}

// Writes multiple coils (function code 15)
func (mc *ModbusClient) WriteCoils(addr uint16, values []bool) error {
	var req *pdu
	var res *pdu
	var quantity uint16
	var encodedValues []byte

	mc.lock.Lock()
	defer mc.lock.Unlock()

	quantity = uint16(len(values))
	if quantity == 0 {
		mc.logger.Error("quantity of coils is 0")
		return ErrUnexpectedParameters
	}

	if quantity > 0x7b0 {
		mc.logger.Error("quantity of coils exceeds 1968")
		return ErrUnexpectedParameters
	}

	if uint32(addr)+uint32(quantity)-1 > 0xffff {
		mc.logger.Error("end coil address is past 0xffff")
		return ErrUnexpectedParameters
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

	res, err := mc.executeRequest(req)
	if err != nil {
		return err
	}

	switch {
	case res.functionCode == req.functionCode:
		if len(res.payload) != 4 ||
			bytesToUint16(BIG_ENDIAN, res.payload[0:2]) != addr ||
			bytesToUint16(BIG_ENDIAN, res.payload[2:4]) != quantity {
			return ErrProtocolError
		}

	case res.functionCode == (req.functionCode | 0x80):
		if len(res.payload) != 1 {
			return ErrProtocolError
		}

		return mapExceptionCodeToError(res.payload[0])

	default:
		mc.logger.Warn(fmt.Sprintf("unexpected response code (%v)", res.functionCode))
		return ErrProtocolError
	}

	return nil
}

// Writes a single 16-bit register (function code 06).
func (mc *ModbusClient) WriteRegister(addr uint16, value uint16) error {
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

	res, err := mc.executeRequest(req)
	if err != nil {
		return err
	}

	switch {
	case res.functionCode == req.functionCode:
		if len(res.payload) != 4 ||
			bytesToUint16(BIG_ENDIAN, res.payload[0:2]) != addr ||
			bytesToUint16(mc.endianness, res.payload[2:4]) != value {
			return ErrProtocolError
		}

	case res.functionCode == (req.functionCode | 0x80):
		if len(res.payload) != 1 {
			return ErrProtocolError
		}

		return mapExceptionCodeToError(res.payload[0])

	default:
		mc.logger.Warn(fmt.Sprintf("unexpected response code (%v)", res.functionCode))
		return ErrProtocolError
	}

	return nil
}

// Writes multiple 16-bit registers (function code 16).
func (mc *ModbusClient) WriteRegisters(addr uint16, values []uint16) error {
	var payload []byte

	for _, value := range values {
		payload = append(payload, uint16ToBytes(mc.endianness, value)...)
	}

	return mc.writeRegisters(addr, payload)
}

// Writes multiple 32-bit registers.
func (mc *ModbusClient) WriteUint32s(addr uint16, values []uint32) error {
	var payload []byte

	for _, value := range values {
		payload = append(payload, uint32ToBytes(mc.endianness, mc.wordOrder, value)...)
	}

	return mc.writeRegisters(addr, payload)
}

// Writes a single 32-bit register.
func (mc *ModbusClient) WriteUint32(addr uint16, value uint32) error {
	return mc.writeRegisters(addr, uint32ToBytes(mc.endianness, mc.wordOrder, value))
}

// Writes multiple 32-bit float registers.
func (mc *ModbusClient) WriteFloat32s(addr uint16, values []float32) error {
	var payload []byte

	for _, value := range values {
		payload = append(payload, float32ToBytes(mc.endianness, mc.wordOrder, value)...)
	}

	return mc.writeRegisters(addr, payload)
}

// Writes a single 32-bit float register.
func (mc *ModbusClient) WriteFloat32(addr uint16, value float32) error {
	return mc.writeRegisters(addr, float32ToBytes(mc.endianness, mc.wordOrder, value))
}

// Writes multiple 64-bit registers.
func (mc *ModbusClient) WriteUint64s(addr uint16, values []uint64) error {
	var payload []byte

	for _, value := range values {
		payload = append(payload, uint64ToBytes(mc.endianness, mc.wordOrder, value)...)
	}

	return mc.writeRegisters(addr, payload)
}

// Writes a single 64-bit register.
func (mc *ModbusClient) WriteUint64(addr uint16, value uint64) error {
	return mc.writeRegisters(addr, uint64ToBytes(mc.endianness, mc.wordOrder, value))
}

// Writes multiple 64-bit float registers.
func (mc *ModbusClient) WriteFloat64s(addr uint16, values []float64) error {
	var payload []byte

	for _, value := range values {
		payload = append(payload, float64ToBytes(mc.endianness, mc.wordOrder, value)...)
	}

	return mc.writeRegisters(addr, payload)
}

// Writes a single 64-bit float register.
func (mc *ModbusClient) WriteFloat64(addr uint16, value float64) error {
	return mc.writeRegisters(addr, float64ToBytes(mc.endianness, mc.wordOrder, value))
}

// Writes the given slice of bytes to 16-bit registers starting at addr.
// A per-register byteswap is performed if endianness is set to LITTLE_ENDIAN.
// Odd byte quantities are padded with a null byte to fall on 16-bit register boundaries.
func (mc *ModbusClient) WriteBytes(addr uint16, values []byte) error {
	return mc.writeBytes(addr, values, true)
}

// Writes the given slice of bytes to 16-bit registers starting at addr.
// No byte or word reordering is performed: bytes are pushed to the wire as-is,
// allowing the caller to handle encoding/endianness/word order manually.
// Odd byte quantities are padded with a null byte to fall on 16-bit register boundaries.
func (mc *ModbusClient) WriteRawBytes(addr uint16, values []byte) error {
	return mc.writeBytes(addr, values, false)
}

/*** unexported methods ***/
// Reads one or multiple 16-bit registers (function code 03 or 04) as bytes.
func (mc *ModbusClient) readBytes(addr uint16, quantity uint16, regType RegType, observeEndianness bool) ([]byte, error) {
	regCount := (quantity / 2) + (quantity % 2)

	values, err := mc.readRegisters(addr, regCount, regType)
	if err != nil {
		return nil, err
	}

	if observeEndianness && mc.endianness == LITTLE_ENDIAN {
		for i := 0; i < len(values); i += 2 {
			values[i], values[i+1] = values[i+1], values[i]
		}
	}

	if quantity%2 == 1 {
		values = values[0 : len(values)-1]
	}

	return values, nil
}

// Writes the given slice of bytes to 16-bit registers starting at addr.
func (mc *ModbusClient) writeBytes(addr uint16, values []byte, observeEndianness bool) error {
	if len(values)%2 == 1 {
		values = append(values, 0x00)
	}

	if observeEndianness && mc.endianness == LITTLE_ENDIAN {
		for i := 0; i < len(values); i += 2 {
			values[i], values[i+1] = values[i+1], values[i]
		}
	}

	return mc.writeRegisters(addr, values)
}

// Reads and returns quantity booleans.
// Digital inputs are read if di is true, otherwise coils are read.
func (mc *ModbusClient) readBools(addr uint16, quantity uint16, di bool) ([]bool, error) {
	var req *pdu
	var res *pdu
	var expectedLen int

	mc.lock.Lock()
	defer mc.lock.Unlock()

	if quantity == 0 {
		mc.logger.Error("quantity of coils/discrete inputs is 0")
		return nil, ErrUnexpectedParameters
	}

	if quantity > 2000 {
		mc.logger.Error("quantity of coils/discrete inputs exceeds 2000")
		return nil, ErrUnexpectedParameters
	}

	if uint32(addr)+uint32(quantity)-1 > 0xffff {
		mc.logger.Error("end coil/discrete input address is past 0xffff")
		return nil, ErrUnexpectedParameters
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

	res, err := mc.executeRequest(req)
	if err != nil {
		return nil, err
	}

	switch {
	case res.functionCode == req.functionCode:
		expectedLen = 1
		expectedLen += int(quantity) / 8
		if quantity%8 != 0 {
			expectedLen++
		}

		if len(res.payload) != expectedLen {
			return nil, ErrProtocolError
		}

		if int(res.payload[0])+1 != expectedLen {
			return nil, ErrProtocolError
		}

		return decodeBools(quantity, res.payload[1:]), nil

	case res.functionCode == (req.functionCode | 0x80):
		if len(res.payload) != 1 {
			return nil, ErrProtocolError
		}

		return nil, mapExceptionCodeToError(res.payload[0])

	default:
		mc.logger.Warn(fmt.Sprintf("unexpected response code (%v)", res.functionCode))
		return nil, ErrProtocolError
	}
}

// Reads and returns quantity registers of type regType, as bytes.
func (mc *ModbusClient) readRegisters(addr uint16, quantity uint16, regType RegType) ([]byte, error) {
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
		mc.logger.Error("unexpected register", "type", regType)
		return nil, ErrUnexpectedParameters
	}

	if quantity == 0 {
		mc.logger.Error("quantity of registers is 0")
		return nil, ErrUnexpectedParameters
	}

	if quantity > 125 {
		mc.logger.Error("quantity of registers exceeds 125")
		return nil, ErrUnexpectedParameters
	}

	if uint32(addr)+uint32(quantity)-1 > 0xffff {
		mc.logger.Error("end register address is past 0xffff")
		return nil, ErrUnexpectedParameters
	}

	req.payload = uint16ToBytes(BIG_ENDIAN, addr)
	req.payload = append(req.payload, uint16ToBytes(BIG_ENDIAN, quantity)...)

	res, err := mc.executeRequest(req)
	if err != nil {
		return nil, err
	}

	switch {
	case res.functionCode == req.functionCode:
		if len(res.payload) != 1+2*int(quantity) {
			return nil, ErrProtocolError
		}

		if uint(res.payload[0]) != 2*uint(quantity) {
			return nil, ErrProtocolError
		}

		return res.payload[1:], nil

	case res.functionCode == (req.functionCode | 0x80):
		if len(res.payload) != 1 {
			return nil, ErrProtocolError
		}

		return nil, mapExceptionCodeToError(res.payload[0])

	default:
		mc.logger.Warn(fmt.Sprintf("unexpected response code (%v)", res.functionCode))
		return nil, ErrProtocolError
	}
}

// Writes multiple registers starting from base address addr.
// Register values are passed as bytes, each value being exactly 2 bytes.
func (mc *ModbusClient) writeRegisters(addr uint16, values []byte) error {
	var req *pdu
	var res *pdu
	var payloadLength uint16
	var quantity uint16

	mc.lock.Lock()
	defer mc.lock.Unlock()

	payloadLength = uint16(len(values))
	quantity = payloadLength / 2

	if quantity == 0 {
		mc.logger.Error("quantity of registers is 0")
		return ErrUnexpectedParameters
	}

	if quantity > 123 {
		mc.logger.Error("quantity of registers exceeds 123")
		return ErrUnexpectedParameters
	}

	if uint32(addr)+uint32(quantity)-1 > 0xffff {
		mc.logger.Error("end register address is past 0xffff")
		return ErrUnexpectedParameters
	}

	req = &pdu{
		unitId:       mc.unitId,
		functionCode: fcWriteMultipleRegisters,
	}

	req.payload = uint16ToBytes(BIG_ENDIAN, addr)
	req.payload = append(req.payload, uint16ToBytes(BIG_ENDIAN, quantity)...)
	req.payload = append(req.payload, byte(payloadLength))
	req.payload = append(req.payload, values...)

	res, err := mc.executeRequest(req)
	if err != nil {
		return err
	}

	switch {
	case res.functionCode == req.functionCode:
		if len(res.payload) != 4 ||
			bytesToUint16(BIG_ENDIAN, res.payload[0:2]) != addr ||
			bytesToUint16(BIG_ENDIAN, res.payload[2:4]) != quantity {
			return ErrProtocolError
		}

	case res.functionCode == (req.functionCode | 0x80):
		if len(res.payload) != 1 {
			return ErrProtocolError
		}

		return mapExceptionCodeToError(res.payload[0])

	default:
		mc.logger.Warn(fmt.Sprintf("unexpected response code (%v)", res.functionCode))
		return ErrProtocolError
	}

	return nil
}

func (mc *ModbusClient) executeRequest(req *pdu) (*pdu, error) {
	res, err := mc.transport.ExecuteRequest(req)
	if err != nil {
		if os.IsTimeout(err) {
			return nil, ErrRequestTimedOut
		}
		return nil, err
	}

	if (res.functionCode&0x80) == 0x00 && res.unitId != req.unitId {
		return nil, ErrBadUnitId
	}
	if (res.functionCode&0x80) == 0x80 &&
		(res.unitId != req.unitId && res.unitId != 0xff) {
		return nil, ErrBadUnitId
	}

	return res, nil
}
