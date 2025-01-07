package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"tcp-over-http/client"
	"tcp-over-http/server"
)

const VERSION = "1.0.0"

func main() {
	fmt.Println("Version: ", VERSION)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-quit:
			cancel()
		}
	}()

	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	server.RegisterLogger(&logger)
	client.RegisterLogger(&logger)

	rootCmd := &cobra.Command{
		Use:   "toh",
		Short: "a simple tcp tunnel transported over http",
	}
	server.RegisterCmd(rootCmd)
	client.RegisterCmd(rootCmd)

	_ = rootCmd.ExecuteContext(ctx)
}
