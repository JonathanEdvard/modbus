package modbus

import (
	"time"

	"github.com/goburrow/serial"
)

// serialPortWrapper wraps a serial.Port (i.e. physical port) to
// 1) satisfy the rtuLink interface and
// 2) add Read() deadline/timeout support.
type serialPortWrapper struct {
	conf     *serialPortConfig
	port     serial.Port
	deadline time.Time
}

type serialPortConfig struct {
	Device   string
	Speed    uint
	DataBits uint8
	Parity   uint8
	StopBits uint8
}

func newSerialPortWrapper(conf *serialPortConfig) *serialPortWrapper {
	return &serialPortWrapper{
		conf: conf,
	}
}

func (spw *serialPortWrapper) Open() error {
	var parity string

	switch spw.conf.Parity {
	case PARITY_NONE:
		parity = "N"
	case PARITY_EVEN:
		parity = "E"
	case PARITY_ODD:
		parity = "O"
	}

	var err error
	spw.port, err = serial.Open(&serial.Config{
		Address:  spw.conf.Device,
		BaudRate: int(spw.conf.Speed),
		DataBits: int(spw.conf.DataBits),
		Parity:   parity,
		StopBits: int(spw.conf.StopBits),
		Timeout:  10 * time.Millisecond,
	})

	return err
}

func (spw *serialPortWrapper) Close() error {
	return spw.port.Close()
}

// Reads bytes from the underlying serial port.
// If Read() is called after the deadline, a timeout error is returned without
// attempting to read from the serial port.
// If Read() is called before the deadline, a read attempt to the serial port
// is made. At this point, one of two things can happen:
//   - the serial port's receive buffer has one or more bytes and port.Read()
//     returns immediately (partial or full read),
//   - the serial port's receive buffer is empty: port.Read() blocks for
//     up to 10ms and returns serial.ErrTimeout. The serial timeout error is
//     masked and Read() returns with no data.
//
// As the higher-level methods use io.ReadFull(), Read() will be called
// as many times as necessary until either enough bytes have been read or an
// error is returned (ErrRequestTimedOut or any other i/o error).
func (spw *serialPortWrapper) Read(rxbuf []byte) (int, error) {
	// return a timeout error if the deadline has passed
	if time.Now().After(spw.deadline) {
		return 0, ErrRequestTimedOut
	}

	cnt, err := spw.port.Read(rxbuf)
	// mask serial.ErrTimeout errors from the serial port
	if err != nil && err == serial.ErrTimeout {
		err = nil
	}

	return cnt, err
}

func (spw *serialPortWrapper) Write(txbuf []byte) (int, error) {
	return spw.port.Write(txbuf)
}

// Saves the i/o deadline (only used by Read).
func (spw *serialPortWrapper) SetDeadline(deadline time.Time) error {
	spw.deadline = deadline

	return nil
}
