package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/spf13/cobra"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var pauseCmd = &cobra.Command{
	Use:   "pause",
	Short: "A command that simply pauses",
	Long:  `Pauses and waits for the SIGTERM signal`,
	Run:   pause,
}

func init() {
	rootCmd.AddCommand(pauseCmd)
	definePauserFlags(pauseCmd)
}

func definePauserFlags(cmd *cobra.Command) {
	cmd.Flags().String("main-container", "", "The main container you should be checking the logs for")

	flags := cmd.Flags()
	flags.AddFlagSet(zap.FlagSet())
}

func pause(cmd *cobra.Command, args []string) {
	exitSignal := make(chan os.Signal, 1)
	signal.Notify(exitSignal, syscall.SIGINT, syscall.SIGTERM)

	ct := getValidStringArg(cmd, "main-container")
	logf.SetLogger(zap.Logger())

	log.Info(fmt.Sprintf("This is merely a 'pause' container. You should instead check the logs of: %s", ct))

	<-exitSignal
}
