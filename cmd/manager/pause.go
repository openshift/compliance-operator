package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

var pauseCmd = &cobra.Command{
	Use:   "pause",
	Short: "A command that simply pauses",
	Long:  `Pauses and waits for the SIGTERM signal`,
	Run:   pause,
}

func init() {
	rootCmd.AddCommand(pauseCmd)
}

func pause(cmd *cobra.Command, args []string) {
	exitSignal := make(chan os.Signal, 1)
	signal.Notify(exitSignal, syscall.SIGINT, syscall.SIGTERM)

	<-exitSignal
}
