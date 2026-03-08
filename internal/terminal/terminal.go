// Package terminal provides serial terminal functionality.
package terminal

import (
	"fmt"
	"io"
	"os"

	"go.bug.st/serial"
)

// Config holds the configuration for a serial terminal session.
type Config struct {
	Port     string
	BaudRate int
	DataBits int
	StopBits serial.StopBits
	Parity   serial.Parity
}

// DefaultConfig returns a Config with common default settings.
func DefaultConfig(port string) Config {
	return Config{
		Port:     port,
		BaudRate: 9600,
		DataBits: 8,
		StopBits: serial.OneStopBit,
		Parity:   serial.NoParity,
	}
}

// Port is the interface used to communicate with a serial device.
type Port interface {
	io.Reader
	io.Writer
	io.Closer
}

// Run opens the serial port and starts an interactive terminal session.
// Data from the serial port is written to out, and input from in is sent to the
// serial port. The session ends when in reaches EOF.
func Run(cfg Config, in io.Reader, out io.Writer) error {
	mode := &serial.Mode{
		BaudRate: cfg.BaudRate,
		DataBits: cfg.DataBits,
		StopBits: cfg.StopBits,
		Parity:   cfg.Parity,
	}

	port, err := serial.Open(cfg.Port, mode)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", cfg.Port, err)
	}

	return RunWithPort(port, cfg, in, out)
}

// RunWithPort starts an interactive terminal session using the provided port.
// This allows callers to inject a mock port for testing.
func RunWithPort(port Port, cfg Config, in io.Reader, out io.Writer) error {
	defer port.Close()

	fmt.Fprintf(out, "Connected to %s at %d baud. Press Ctrl+C to exit.\n", cfg.Port, cfg.BaudRate)

	errc := make(chan error, 1)

	// Read from serial port and write to output.
	go func() {
		_, err := io.Copy(out, port)
		errc <- err
	}()

	// Read from input and write to serial port.
	go func() {
		_, err := io.Copy(port, in)
		errc <- err
	}()

	if err := <-errc; err != nil && err != io.EOF {
		return fmt.Errorf("serial session error: %w", err)
	}
	return nil
}

// RunInteractive opens the serial port and starts an interactive session using
// stdin and stdout.
func RunInteractive(cfg Config) error {
	return Run(cfg, os.Stdin, os.Stdout)
}
