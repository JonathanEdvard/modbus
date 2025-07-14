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

func (spw *serialPortWrapper) Read(rxbuf []byte) (int, error) {
	if time.Now().After(spw.deadline) {
		return 0, ErrRequestTimedOut
	}

	cnt, err := spw.port.Read(rxbuf)
	if err != nil && err == serial.ErrTimeout {
		err = nil
	}

	return cnt, err
}

func (spw *serialPortWrapper) Write(txbuf []byte) (int, error) {
	return spw.port.Write(txbuf)
}

func (spw *serialPortWrapper) SetDeadline(deadline time.Time) error {
	spw.deadline = deadline

	return nil
}
