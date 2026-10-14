// Command wt manages a read-only main clone plus one git worktree and Claude session per task.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/martbock/wt/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)

	err := cli.Execute(ctx)
	stop()

	if err != nil {
		fmt.Fprintln(os.Stderr, "wt:", err)
		os.Exit(1)
	}
}
