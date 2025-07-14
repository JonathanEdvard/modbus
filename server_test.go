package modbus

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

type mockHandler struct{}

func (m *mockHandler) HandleCoils(req *CoilsRequest) ([]bool, error) {
	if req.IsWrite {
		return nil, nil
	}
	return []bool{true, false}, nil
}

func (m *mockHandler) HandleDiscreteInputs(req *DiscreteInputsRequest) ([]bool, error) {
	return []bool{true}, nil
}

func (m *mockHandler) HandleHoldingRegisters(req *HoldingRegistersRequest) ([]uint16, error) {
	if req.IsWrite {
		return nil, nil
	}
	return []uint16{0xABCD}, nil
}

func (m *mockHandler) HandleInputRegisters(req *InputRegistersRequest) ([]uint16, error) {
	return []uint16{0x1234}, nil
}

func TestNewServer(t *testing.T) {
	conf := &ServerConfiguration{URL: "tcp://:502"}
	_, err := NewServer(conf, &mockHandler{})
	if err != nil {
		t.Errorf("NewServer error = %v", err)
	}
}

func TestServerStartStop(t *testing.T) {
	conf := &ServerConfiguration{
		URL:     "tcp://127.0.0.1:0",
		Logger:  slog.New(slog.NewTextHandler(os.Stdout, nil)),
		Timeout: time.Second,
	}
	ms, err := NewServer(conf, &mockHandler{})
	if err != nil {
		t.Fatal(err)
	}

	err = ms.Start(context.Background())
	if err != nil {
		t.Errorf("Start error = %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	err = ms.Stop()
	if err != nil {
		t.Errorf("Stop error = %v", err)
	}
}
