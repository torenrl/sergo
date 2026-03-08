package serial

import (
	"fmt"
	"io"

	goserial "go.bug.st/serial"
)

type Config struct {
	BaudRate int
	DataBits int
	StopBits StopBits
	Parity   Parity
}

type StopBits int

const (
	OneStopBit      StopBits = iota
	OnePointFiveStopBits
	TwoStopBits
)

type Parity int

const (
	NoParity Parity = iota
	OddParity
	EvenParity
	MarkParity
	SpaceParity
)

func DefaultConfig() Config {
	return Config{
		BaudRate: 115200,
		DataBits: 8,
		StopBits: OneStopBit,
		Parity:   NoParity,
	}
}

type Port struct {
	name   string
	port   goserial.Port
	config Config
}

func ListPorts() ([]string, error) {
	ports, err := goserial.GetPortsList()
	if err != nil {
		return nil, fmt.Errorf("listing serial ports: %w", err)
	}
	return ports, nil
}

func Open(name string, cfg Config) (*Port, error) {
	mode := &goserial.Mode{
		BaudRate: cfg.BaudRate,
		DataBits: cfg.DataBits,
		StopBits: toLibStopBits(cfg.StopBits),
		Parity:   toLibParity(cfg.Parity),
	}

	p, err := goserial.Open(name, mode)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", name, err)
	}

	return &Port{name: name, port: p, config: cfg}, nil
}

func (p *Port) Read(buf []byte) (int, error) {
	return p.port.Read(buf)
}

func (p *Port) Write(data []byte) (int, error) {
	return p.port.Write(data)
}

func (p *Port) Close() error {
	return p.port.Close()
}

func (p *Port) Name() string {
	return p.name
}

func (p *Port) Config() Config {
	return p.config
}

// ReadWriteCloser returns the port as an io.ReadWriteCloser for
// generic use by any frontend.
func (p *Port) ReadWriteCloser() io.ReadWriteCloser {
	return p.port
}

func toLibStopBits(sb StopBits) goserial.StopBits {
	switch sb {
	case OnePointFiveStopBits:
		return goserial.OnePointFiveStopBits
	case TwoStopBits:
		return goserial.TwoStopBits
	default:
		return goserial.OneStopBit
	}
}

func toLibParity(par Parity) goserial.Parity {
	switch par {
	case OddParity:
		return goserial.OddParity
	case EvenParity:
		return goserial.EvenParity
	case MarkParity:
		return goserial.MarkParity
	case SpaceParity:
		return goserial.SpaceParity
	default:
		return goserial.NoParity
	}
}
