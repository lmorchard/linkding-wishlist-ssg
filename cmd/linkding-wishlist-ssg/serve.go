package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/config"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/server"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/siteconfig"
)

func (a *application) runServe(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: linkding-wishlist-ssg serve [flags]

Starts a local HTTP static file server to preview the generated site.

Flags:
`)
		fs.PrintDefaults()
	}

	fs.String("dir", "./build", "directory of static files to serve (falls back to --out value)")
	fs.String("addr", ":8080", "address and port to bind")

	if wantsHelp(args) {
		fs.SetOutput(a.stdout)
		fs.Usage()
		return 0
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(a.stderr, "error: unexpected argument %q\n", fs.Arg(0))
		return 1
	}

	envFile := map[string]string{}
	if a.envFilePath != "" {
		if loaded, err := config.LoadEnv(a.envFilePath); err == nil {
			envFile = loaded
		}
	}
	resolve := siteconfig.NewResolver(fs, a.lookupEnv, envFile, func(msg string) {
		fmt.Fprintf(a.stderr, "warning: %s\n", msg)
	})

	dir := resolve.String("dir")
	// If --dir was default or empty, check if --out was configured via env or flag
	if dir == "./build" || dir == "" {
		if out := resolve.String("out"); out != "" && out != "./build" {
			dir = out
		}
	}

	addr := resolve.String("addr")

	opts := server.Options{
		Dir:  dir,
		Addr: addr,
	}

	if err := server.Serve(ctx, opts, a.stdout, a.stderr); err != nil {
		fmt.Fprintf(a.stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
