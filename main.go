package main

import (
	"context"
	"fmt"
	"os"

	"github.com/paularlott/llmrouter/cmd"
)

func main() {
	if err := cmd.RootCmd.Execute(context.Background()); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}
