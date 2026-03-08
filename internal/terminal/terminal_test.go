package terminal_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/torenrl/sergo/internal/terminal"
	"go.bug.st/serial"
)

// mockPort implements io.ReadWriteCloser to simulate a serial port.
type mockPort struct {
	reader io.Reader
	writer io.Writer
}

func (m *mockPort) Read(p []byte) (int, error)  { return m.reader.Read(p) }
func (m *mockPort) Write(p []byte) (int, error) { return m.writer.Write(p) }
func (m *mockPort) Close() error                { return nil }

func TestDefaultConfig(t *testing.T) {
	cfg := terminal.DefaultConfig("/dev/ttyUSB0")

	if cfg.Port != "/dev/ttyUSB0" {
		t.Errorf("expected port /dev/ttyUSB0, got %s", cfg.Port)
	}
	if cfg.BaudRate != 9600 {
		t.Errorf("expected baud rate 9600, got %d", cfg.BaudRate)
	}
	if cfg.DataBits != 8 {
		t.Errorf("expected data bits 8, got %d", cfg.DataBits)
	}
	if cfg.StopBits != serial.OneStopBit {
		t.Errorf("expected OneStopBit, got %v", cfg.StopBits)
	}
	if cfg.Parity != serial.NoParity {
		t.Errorf("expected NoParity, got %v", cfg.Parity)
	}
}

func TestRun_CopiesSerialOutputToWriter(t *testing.T) {
	serialData := "hello from device\n"

	// Serial port sends data, then EOF.
	portReader := strings.NewReader(serialData)
	var portWriteBuf bytes.Buffer
	mp := &mockPort{reader: portReader, writer: &portWriteBuf}

	var out bytes.Buffer
	// Use a pipe as stdin so it blocks indefinitely. The session ends when the
	// serial port reader returns EOF, which signals that all data has been
	// forwarded to out.
	stdinR, stdinW := io.Pipe()
	defer stdinW.Close()

	err := terminal.RunWithPort(mp, terminal.Config{Port: "mock", BaudRate: 9600}, stdinR, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out.String(), serialData) {
		t.Errorf("output %q does not contain serial data %q", out.String(), serialData)
	}
}

func TestRun_CopiesInputToSerialPort(t *testing.T) {
	// Serial port reader blocks indefinitely so the session ends only when
	// stdin reaches EOF (input goroutine finishes first).
	pr, pw := io.Pipe()
	defer pw.Close()

	var portWriteBuf bytes.Buffer
	mp := &mockPort{reader: pr, writer: &portWriteBuf}

	input := "AT\r\n"
	in := strings.NewReader(input)
	var out bytes.Buffer

	err := terminal.RunWithPort(mp, terminal.Config{Port: "mock", BaudRate: 9600}, in, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if portWriteBuf.String() != input {
		t.Errorf("expected %q written to port, got %q", input, portWriteBuf.String())
	}
}
