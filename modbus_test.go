package modbus

import (
	"testing"
)

func TestMapExceptionCodeToError(t *testing.T) {
	tests := []struct {
		code uint8
		want Error
	}{
		{exIllegalFunction, ErrIllegalFunction},
	}

	for _, tt := range tests {
		got := mapExceptionCodeToError(tt.code)
		if got != tt.want {
			t.Errorf("mapExceptionCodeToError(%d) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestMapErrorToExceptionCode(t *testing.T) {
	tests := []struct {
		err  error
		want uint8
	}{
		{ErrIllegalFunction, exIllegalFunction},
	}

	for _, tt := range tests {
		got := mapErrorToExceptionCode(tt.err)
		if got != tt.want {
			t.Errorf("mapErrorToExceptionCode(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}
