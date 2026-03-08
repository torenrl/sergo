package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.bug.st/serial"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List available serial ports",
	RunE:  runList,
}

func init() {
	rootCmd.AddCommand(listCmd)
}

func runList(_ *cobra.Command, _ []string) error {
	ports, err := serial.GetPortsList()
	if err != nil {
		return fmt.Errorf("failed to list serial ports: %w", err)
	}
	if len(ports) == 0 {
		fmt.Println("No serial ports found.")
		return nil
	}
	for _, port := range ports {
		fmt.Println(port)
	}
	return nil
}
