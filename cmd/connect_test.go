package cmd

import (
	"testing"

	"go.bug.st/serial"
)

func TestParseStopBits(t *testing.T) {
	tests := []struct {
		input    string
		expected serial.StopBits
		wantErr  bool
	}{
		{"1", serial.OneStopBit, false},
		{"1.5", serial.OnePointFiveStopBits, false},
		{"2", serial.TwoStopBits, false},
		{"3", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		got, err := parseStopBits(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseStopBits(%q): expected error, got nil", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseStopBits(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.expected {
			t.Errorf("parseStopBits(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestParseParity(t *testing.T) {
	tests := []struct {
		input    string
		expected serial.Parity
		wantErr  bool
	}{
		{"none", serial.NoParity, false},
		{"n", serial.NoParity, false},
		{"odd", serial.OddParity, false},
		{"o", serial.OddParity, false},
		{"even", serial.EvenParity, false},
		{"e", serial.EvenParity, false},
		{"mark", serial.MarkParity, false},
		{"m", serial.MarkParity, false},
		{"space", serial.SpaceParity, false},
		{"s", serial.SpaceParity, false},
		{"NONE", serial.NoParity, false},
		{"ODD", serial.OddParity, false},
		{"invalid", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		got, err := parseParity(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseParity(%q): expected error, got nil", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseParity(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.expected {
			t.Errorf("parseParity(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestParseBaudRate(t *testing.T) {
	valid := []int{300, 600, 1200, 2400, 4800, 9600, 14400, 19200, 28800, 38400, 57600, 115200, 230400, 460800, 921600}
	for _, b := range valid {
		got, err := ParseBaudRate(b)
		if err != nil {
			t.Errorf("ParseBaudRate(%d): unexpected error: %v", b, err)
			continue
		}
		if got != b {
			t.Errorf("ParseBaudRate(%d) = %d, want %d", b, got, b)
		}
	}

	_, err := ParseBaudRate(1234)
	if err == nil {
		t.Error("ParseBaudRate(1234): expected error for invalid baud rate, got nil")
	}
}
