package modbus

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

// Modbus Role PEM OID (see R-21 of the MBAPS spec)
var modbusRoleOID asn1.ObjectIdentifier = asn1.ObjectIdentifier{
	1, 3, 6, 1, 4, 1, 50316, 802, 1,
}

// Server configuration object.
type ServerConfiguration struct {
	URL           string
	Timeout       time.Duration
	MaxClients    uint
	TLSServerCert *tls.Certificate
	TLSClientCAs  *x509.CertPool
	Logger        *slog.Logger
	Listen        func(url string) (net.Listener, error)
}

// Request object passed to the coil handler.
type CoilsRequest struct {
	ClientAddr string
	ClientRole string
	UnitId     uint8
	Addr       uint16
	Quantity   uint16
	IsWrite    bool
	Args       []bool
}

// Request object passed to the discrete input handler.
type DiscreteInputsRequest struct {
	ClientAddr string
	ClientRole string
	UnitId     uint8
	Addr       uint16
	Quantity   uint16
}

// Request object passed to the holding register handler.
type HoldingRegistersRequest struct {
	ClientAddr string
	ClientRole string
	UnitId     uint8
	Addr       uint16
	Quantity   uint16
	IsWrite    bool
	Args       []uint16
}

// Request object passed to the input register handler.
type InputRegistersRequest struct {
	ClientAddr string
	ClientRole string
	UnitId     uint8
	Addr       uint16
	Quantity   uint16
}

// The RequestHandler interface should be implemented by the handler
// object passed to NewServer (see reqHandler in NewServer()).
// After decoding and validating an incoming request, the server will
// invoke the appropriate handler function, depending on the function code
// of the request.
type RequestHandler interface {
	HandleCoils(req *CoilsRequest) (res []bool, err error)
	HandleDiscreteInputs(req *DiscreteInputsRequest) (res []bool, err error)
	HandleHoldingRegisters(req *HoldingRegistersRequest) (res []uint16, err error)
	HandleInputRegisters(req *InputRegistersRequest) (res []uint16, err error)
}

// Modbus server object.
type ModbusServer struct {
	conf          ServerConfiguration
	logger        *slog.Logger
	lock          sync.Mutex
	started       bool
	handler       RequestHandler
	tcpListener   net.Listener
	tcpClients    []net.Conn
	transportType transportType
	ctx           context.Context
	cancel        context.CancelFunc
}

// Returns a new modbus server.
func NewServer(conf *ServerConfiguration, reqHandler RequestHandler) (
	ms *ModbusServer, err error) {
	var serverType string
	var splitURL []string

	ms = &ModbusServer{
		conf:    *conf,
		handler: reqHandler,
	}

	splitURL = strings.SplitN(ms.conf.URL, "://", 2)
	if len(splitURL) == 2 {
		serverType = splitURL[0]
		ms.conf.URL = splitURL[1]
	}

	ms.logger = ms.conf.Logger

	if ms.conf.URL == "" {
		ms.logger.Error("missing host part in URL", "url", conf.URL)
		err = ErrConfigurationError
		return
	}

	if ms.conf.Listen == nil {
		ms.conf.Listen = func(url string) (net.Listener, error) {
			return net.Listen("tcp", url)
		}
	}

	switch serverType {
	case "tcp":
		if ms.conf.Timeout == 0 {
			ms.conf.Timeout = 120 * time.Second
		}

		if ms.conf.MaxClients == 0 {
			ms.conf.MaxClients = 10
		}

		ms.transportType = modbusTCP

	case "tcp+tls":
		if ms.conf.Timeout == 0 {
			ms.conf.Timeout = 120 * time.Second
		}

		if ms.conf.MaxClients == 0 {
			ms.conf.MaxClients = 10
		}

		if ms.conf.TLSServerCert == nil {
			ms.logger.Error("missing server certificate")
			err = ErrConfigurationError
			return
		}

		if ms.conf.TLSClientCAs == nil {
			ms.logger.Error("missing CA/client certificates")
			err = ErrConfigurationError
			return
		}

		ms.transportType = modbusTCPOverTLS

	default:
		err = ErrConfigurationError
		return
	}

	return
}

// Starts accepting client connections.
func (ms *ModbusServer) Start(ctx context.Context) (err error) {
	ms.lock.Lock()
	defer ms.lock.Unlock()

	if ms.started {
		return
	}

	ms.ctx, ms.cancel = context.WithCancel(ctx)

	switch ms.transportType {
	case modbusTCP, modbusTCPOverTLS:
		ms.tcpListener, err = ms.conf.Listen(ms.conf.URL)
		if err != nil {
			return
		}

		go ms.acceptTCPClients()

	default:
		err = ErrConfigurationError
		return
	}

	ms.started = true

	return
}

// Stops accepting new client connections and closes any active session.
func (ms *ModbusServer) Stop() (err error) {
	ms.lock.Lock()
	defer ms.lock.Unlock()

	if !ms.started {
		return
	}

	ms.started = false
	ms.cancel()

	if ms.transportType == modbusTCP || ms.transportType == modbusTCPOverTLS {
		err = ms.tcpListener.Close()

		for _, sock := range ms.tcpClients {
			sock.Close()
		}
	}

	return
}

// Accepts new client connections if the configured connection limit allows it.
// Each connection is served from a dedicated goroutine to allow for concurrent
// connections.
func (ms *ModbusServer) acceptTCPClients() {
	var sock net.Conn
	var err error
	var accepted bool

	for {
		select {
		case <-ms.ctx.Done():
			return
		default:
			sock, err = ms.tcpListener.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					break
				}
				ms.logger.Warn("failed to accept client connection", "err", err)
				continue
			}

			ms.lock.Lock()
			if ms.started && uint(len(ms.tcpClients)) < ms.conf.MaxClients {
				accepted = true
				ms.tcpClients = append(ms.tcpClients, sock)
			} else {
				accepted = false
			}
			ms.lock.Unlock()

			if accepted {
				go ms.handleTCPClient(sock)
			} else {
				ms.logger.Warn("max. number of concurrent connections "+
					"reached, rejecting", "remoteAddr", sock.RemoteAddr())
				sock.Close()
			}
		}
	}
}

// Handles a TCP client connection.
// Once handleTransport() returns (i.e. the connection has either closed, timed
// out, or an unrecoverable error happened), the TCP socket is closed and removed
// from the list of active client connections.
func (ms *ModbusServer) handleTCPClient(sock net.Conn) {
	var err error
	var clientRole string
	var tlsSock net.Conn

	switch ms.transportType {
	case modbusTCP:
		ms.handleTransport(
			newTCPTransport(sock, ms.conf.Timeout, ms.conf.Logger),
			sock.RemoteAddr().String(), "")

	case modbusTCPOverTLS:
		tlsSock, clientRole, err = ms.startTLS(sock)
		if err != nil {
			ms.logger.Warn("TLS handshake failed", "client", sock.RemoteAddr().String(), "err", err)
		} else {
			ms.handleTransport(
				newTCPTransport(tlsSock, ms.conf.Timeout, ms.conf.Logger),
				sock.RemoteAddr().String(), clientRole)
		}
	}

	ms.lock.Lock()
	for i := range ms.tcpClients {
		if ms.tcpClients[i] == sock {
			ms.tcpClients[i] = ms.tcpClients[len(ms.tcpClients)-1]
			ms.tcpClients = ms.tcpClients[:len(ms.tcpClients)-1]
			break
		}
	}
	ms.lock.Unlock()

	sock.Close()
}

// For each request read from the transport, performs decoding and validation,
// calls the user-provided handler, then encodes and writes the response
// to the transport.
func (ms *ModbusServer) handleTransport(t transport, clientAddr string, clientRole string) {
	var req *pdu
	var res *pdu
	var err error
	var addr uint16
	var quantity uint16

	for {
		req, err = t.ReadRequest()
		if err != nil {
			break
		}

		switch req.functionCode {
		case fcReadCoils, fcReadDiscreteInputs:
			if len(req.payload) != 4 {
				err = ErrProtocolError
				break
			}

			addr = bytesToUint16(BIG_ENDIAN, req.payload[0:2])
			quantity = bytesToUint16(BIG_ENDIAN, req.payload[2:4])

			if quantity > 2000 || quantity == 0 {
				err = ErrProtocolError
				break
			}
			if uint32(addr)+uint32(quantity)-1 > 0xffff {
				err = ErrIllegalDataAddress
				break
			}

			var coils []bool
			if req.functionCode == fcReadCoils {
				coils, err = ms.handler.HandleCoils(&CoilsRequest{
					ClientAddr: clientAddr,
					ClientRole: clientRole,
					UnitId:     req.unitId,
					Addr:       addr,
					Quantity:   quantity,
					IsWrite:    false,
					Args:       nil,
				})
			} else {
				coils, err = ms.handler.HandleDiscreteInputs(
					&DiscreteInputsRequest{
						ClientAddr: clientAddr,
						ClientRole: clientRole,
						UnitId:     req.unitId,
						Addr:       addr,
						Quantity:   quantity,
					})
			}

			if err == nil && len(coils) != int(quantity) {
				ms.logger.Error("unexpected number of coils returned", "got", len(coils), "expected", quantity)
				err = ErrServerDeviceFailure
				break
			}

			if err != nil {
				break
			}

			res = &pdu{
				unitId:       req.unitId,
				functionCode: req.functionCode,
				payload:      []byte{0},
			}

			res.payload[0] = uint8(len(coils) / 8)
			if len(coils)%8 != 0 {
				res.payload[0]++
			}

			res.payload = append(res.payload, encodeBools(coils)...)

		case fcWriteSingleCoil:
			if len(req.payload) != 4 {
				err = ErrProtocolError
				break
			}

			addr = bytesToUint16(BIG_ENDIAN, req.payload[0:2])

			if (req.payload[2] != 0xff && req.payload[2] != 0x00) ||
				req.payload[3] != 0x00 {
				err = ErrProtocolError
				break
			}

			_, err = ms.handler.HandleCoils(&CoilsRequest{
				ClientAddr: clientAddr,
				ClientRole: clientRole,
				UnitId:     req.unitId,
				Addr:       addr,
				Quantity:   1,
				IsWrite:    true,
				Args:       []bool{(req.payload[2] == 0xff)},
			})

			if err != nil {
				break
			}

			res = &pdu{
				unitId:       req.unitId,
				functionCode: req.functionCode,
			}

			res.payload = append(res.payload,
				uint16ToBytes(BIG_ENDIAN, addr)...)
			res.payload = append(res.payload,
				req.payload[2], req.payload[3])

		case fcWriteMultipleCoils:
			var expectedLen int

			if len(req.payload) < 6 {
				err = ErrProtocolError
				break
			}

			addr = bytesToUint16(BIG_ENDIAN, req.payload[0:2])
			quantity = bytesToUint16(BIG_ENDIAN, req.payload[2:4])

			if quantity > 0x7b0 || quantity == 0 {
				err = ErrProtocolError
				break
			}
			if uint32(addr)+uint32(quantity)-1 > 0xffff {
				err = ErrIllegalDataAddress
				break
			}

			expectedLen = int(quantity) / 8
			if quantity%8 != 0 {
				expectedLen++
			}

			if req.payload[4] != uint8(expectedLen) {
				err = ErrProtocolError
				break
			}

			if len(req.payload)-5 != expectedLen {
				err = ErrProtocolError
				break
			}

			_, err = ms.handler.HandleCoils(&CoilsRequest{
				ClientAddr: clientAddr,
				ClientRole: clientRole,
				UnitId:     req.unitId,
				Addr:       addr,
				Quantity:   quantity,
				IsWrite:    true,
				Args:       decodeBools(quantity, req.payload[5:]),
			})

			if err != nil {
				break
			}

			res = &pdu{
				unitId:       req.unitId,
				functionCode: req.functionCode,
			}

			res.payload = append(res.payload,
				uint16ToBytes(BIG_ENDIAN, addr)...)
			res.payload = append(res.payload,
				uint16ToBytes(BIG_ENDIAN, quantity)...)

		case fcReadHoldingRegisters, fcReadInputRegisters:
			if len(req.payload) != 4 {
				err = ErrProtocolError
				break
			}

			addr = bytesToUint16(BIG_ENDIAN, req.payload[0:2])
			quantity = bytesToUint16(BIG_ENDIAN, req.payload[2:4])

			if quantity > 0x007d || quantity == 0 {
				err = ErrProtocolError
				break
			}
			if uint32(addr)+uint32(quantity)-1 > 0xffff {
				err = ErrIllegalDataAddress
				break
			}

			var regs []uint16
			if req.functionCode == fcReadHoldingRegisters {
				regs, err = ms.handler.HandleHoldingRegisters(
					&HoldingRegistersRequest{
						ClientAddr: clientAddr,
						ClientRole: clientRole,
						UnitId:     req.unitId,
						Addr:       addr,
						Quantity:   quantity,
						IsWrite:    false,
						Args:       nil,
					})
			} else {
				regs, err = ms.handler.HandleInputRegisters(
					&InputRegistersRequest{
						ClientAddr: clientAddr,
						ClientRole: clientRole,
						UnitId:     req.unitId,
						Addr:       addr,
						Quantity:   quantity,
					})
			}

			if err == nil && len(regs) != int(quantity) {
				ms.logger.Error("unexpected number of registers returned", "got", len(regs), "expected", quantity)
				err = ErrServerDeviceFailure
				break
			}

			if err != nil {
				break
			}

			res = &pdu{
				unitId:       req.unitId,
				functionCode: req.functionCode,
				payload:      []byte{0},
			}

			res.payload[0] = uint8(len(regs) * 2)

			res.payload = append(res.payload,
				uint16sToBytes(BIG_ENDIAN, regs)...)

		case fcWriteSingleRegister:
			if len(req.payload) != 4 {
				err = ErrProtocolError
				break
			}

			addr = bytesToUint16(BIG_ENDIAN, req.payload[0:2])
			value := bytesToUint16(BIG_ENDIAN, req.payload[2:4])

			_, err = ms.handler.HandleHoldingRegisters(
				&HoldingRegistersRequest{
					ClientAddr: clientAddr,
					ClientRole: clientRole,
					UnitId:     req.unitId,
					Addr:       addr,
					Quantity:   1,
					IsWrite:    true,
					Args:       []uint16{value},
				})

			if err != nil {
				break
			}

			res = &pdu{
				unitId:       req.unitId,
				functionCode: req.functionCode,
			}

			res.payload = append(res.payload,
				uint16ToBytes(BIG_ENDIAN, addr)...)
			res.payload = append(res.payload,
				uint16ToBytes(BIG_ENDIAN, value)...)

		case fcWriteMultipleRegisters:
			var expectedLen int

			if len(req.payload) < 6 {
				err = ErrProtocolError
				break
			}

			addr = bytesToUint16(BIG_ENDIAN, req.payload[0:2])
			quantity = bytesToUint16(BIG_ENDIAN, req.payload[2:4])

			if quantity > 0x007b || quantity == 0 {
				err = ErrProtocolError
				break
			}
			if uint32(addr)+uint32(quantity)-1 > 0xffff {
				err = ErrIllegalDataAddress
				break
			}

			expectedLen = int(quantity) * 2

			if req.payload[4] != uint8(expectedLen) {
				err = ErrProtocolError
				break
			}

			if len(req.payload)-5 != expectedLen {
				err = ErrProtocolError
				break
			}

			_, err = ms.handler.HandleHoldingRegisters(
				&HoldingRegistersRequest{
					ClientAddr: clientAddr,
					ClientRole: clientRole,
					UnitId:     req.unitId,
					Addr:       addr,
					Quantity:   quantity,
					IsWrite:    true,
					Args:       bytesToUint16s(BIG_ENDIAN, req.payload[5:]),
				})
			if err != nil {
				break
			}

			res = &pdu{
				unitId:       req.unitId,
				functionCode: req.functionCode,
			}

			res.payload = append(res.payload,
				uint16ToBytes(BIG_ENDIAN, addr)...)
			res.payload = append(res.payload,
				uint16ToBytes(BIG_ENDIAN, quantity)...)

		default:
			res = &pdu{
				unitId:       req.unitId,
				functionCode: (0x80 | req.functionCode),
				payload:      []byte{exIllegalFunction},
			}
		}

		if err == nil && res == nil {
			err = ErrServerDeviceFailure
			ms.logger.Error("internal server error", "req", req)
		}

		if err != nil {
			if err == ErrProtocolError {
				ms.logger.Warn("protocol error, closing link", "clientAddr", clientAddr, "req", req)
				t.Close()
				return
			} else {
				res = &pdu{
					unitId:       req.unitId,
					functionCode: (0x80 | req.functionCode),
					payload:      []byte{mapErrorToExceptionCode(err)},
				}
			}
		}

		err = t.WriteResponse(res)
		if err != nil {
			ms.logger.Warn("failed to write response", "err", err)
		}

		req = nil
		res = nil
	}

	ms.logger.Info("closing transport", "clientAddr", clientAddr)
}

// startTLS performs a full TLS handshake (with client authentication) on tcpSock
// and returns a 'wrapped' clear-text socket suitable for use by the TCP transport.
func (ms *ModbusServer) startTLS(tcpSock net.Conn) (
	tlsSock *tls.Conn, clientRole string, err error) {
	var connState tls.ConnectionState

	err = tcpSock.SetDeadline(time.Now().Add(30 * time.Second))
	if err != nil {
		return
	}

	tlsSock = tls.Server(tcpSock, &tls.Config{
		Certificates: []tls.Certificate{
			*ms.conf.TLSServerCert,
		},
		ClientCAs:  ms.conf.TLSClientCAs,
		ClientAuth: tls.RequireAndVerifyClientCert,
		MinVersion: tls.VersionTLS12,
	})

	err = tlsSock.Handshake()
	if err != nil {
		return
	}

	connState = tlsSock.ConnectionState()
	if len(connState.PeerCertificates) == 0 {
		err = errors.New("no client certificate received")
		return
	}
	clientRole = ms.extractRole(connState.PeerCertificates[0])

	return
}

// extractRole looks for Modbus Role extensions in a certificate and returns the
// role as a string.
// If no role extension is found, a nil string is returned (R-23).
// If multiple or invalid role extensions are found, a nil string is returned (R-65, R-22).
func (ms *ModbusServer) extractRole(cert *x509.Certificate) (role string) {
	var err error
	var found bool
	var badCert bool

	for _, ext := range cert.Extensions {
		if ext.Id.Equal(modbusRoleOID) {
			if found {
				ms.logger.Warn("client certificate contains more than one role OIDs")
				badCert = true
				break
			}
			found = true

			if len(ext.Value) < 2 || ext.Value[0] != 0x0c {
				badCert = true
				break
			}

			_, err = asn1.Unmarshal(ext.Value, &role)
			if err != nil {
				ms.logger.Warn("failed to decode Modbus Role extension", "err", err)
				badCert = true
				break
			}
		}
	}

	if badCert {
		role = ""
	}

	return
}
