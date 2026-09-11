package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/rumpl/daw/internal/pluginoci"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 || os.Args[1] != "push" {
		return fmt.Errorf("usage: daw-plugin push [--tag reference] <plugin-directory>")
	}
	flags := flag.NewFlagSet("push", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	tag := flags.String("tag", "", "OCI reference to push, for example docker.io/user/plugin:1.0.0")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *tag == "" || flags.NArg() != 1 {
		return fmt.Errorf("push requires --tag and one plugin directory")
	}
	ref, digest, err := pluginoci.Push(context.Background(), flags.Arg(0), *tag)
	if err != nil {
		return err
	}
	fmt.Printf("pushed %s@%s\n", ref.Context().Name(), digest.String())
	return nil
}
