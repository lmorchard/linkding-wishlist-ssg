package main

import (
	"context"
	"os"
	"time"
)

func main() {
	app := &application{
		stdout: os.Stdout,
		stderr: os.Stderr,
		getenv: os.Getenv,
		now:    time.Now,
	}
	os.Exit(app.run(context.Background(), os.Args[1:]))
}
