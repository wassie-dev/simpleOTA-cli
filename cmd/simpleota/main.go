// Command simpleota is the SimpleOTA command-line tool.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/wassie-dev/simpleOTA-cli/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.NewApp().Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
