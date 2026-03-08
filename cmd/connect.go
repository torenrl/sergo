package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"go.bug.st/serial"

	"github.com/torenrl/sergo/internal/terminal"
)

var (
	baudRate int
	dataBits int
	stopBits string
	parity   string
)

var connectCmd = &cobra.Command{
	Use:   "connect <port>",
	Short: "Connect to a serial port",
	Long: `Connect to a serial port and start an interactive terminal session.

Examples:
  sergo connect /dev/ttyUSB0
  sergo connect /dev/ttyUSB0 --baud 115200
  sergo connect COM3 --baud 9600 --data-bits 8 --stop-bits 1 --parity none`,
	Args: cobra.ExactArgs(1),
	RunE: runConnect,
}

func init() {
	rootCmd.AddCommand(connectCmd)

	connectCmd.Flags().IntVarP(&baudRate, "baud", "b", 9600, "Baud rate")
	connectCmd.Flags().IntVar(&dataBits, "data-bits", 8, "Data bits (5, 6, 7, 8)")
	connectCmd.Flags().StringVar(&stopBits, "stop-bits", "1", "Stop bits (1, 1.5, 2)")
	connectCmd.Flags().StringVar(&parity, "parity", "none", "Parity (none, odd, even, mark, space)")
}

func runConnect(_ *cobra.Command, args []string) error {
	port := args[0]

	sb, err := parseStopBits(stopBits)
	if err != nil {
		return err
	}

	par, err := parseParity(parity)
	if err != nil {
		return err
	}

	cfg := terminal.Config{
		Port:     port,
		BaudRate: baudRate,
		DataBits: dataBits,
		StopBits: sb,
		Parity:   par,
	}

	return terminal.RunInteractive(cfg)
}

func parseStopBits(s string) (serial.StopBits, error) {
	switch strings.TrimSpace(s) {
	case "1":
		return serial.OneStopBit, nil
	case "1.5":
		return serial.OnePointFiveStopBits, nil
	case "2":
		return serial.TwoStopBits, nil
	default:
		return 0, fmt.Errorf("invalid stop bits %q: must be 1, 1.5, or 2", s)
	}
}

func parseParity(s string) (serial.Parity, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none", "n":
		return serial.NoParity, nil
	case "odd", "o":
		return serial.OddParity, nil
	case "even", "e":
		return serial.EvenParity, nil
	case "mark", "m":
		return serial.MarkParity, nil
	case "space", "s":
		return serial.SpaceParity, nil
	default:
		return 0, fmt.Errorf("invalid parity %q: must be none, odd, even, mark, or space", s)
	}
}

// ParseBaudRate validates that the given integer is a recognized baud rate.
func ParseBaudRate(b int) (int, error) {
	valid := []int{300, 600, 1200, 2400, 4800, 9600, 14400, 19200, 28800, 38400, 57600, 115200, 230400, 460800, 921600}
	for _, v := range valid {
		if b == v {
			return b, nil
		}
	}
	return 0, fmt.Errorf("unrecognized baud rate %d", b)
}
