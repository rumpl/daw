// Command tsgen writes the TypeScript mirror of internal/protocol.
package main

import (
	"fmt"
	"os"

	"github.com/rumpl/daw/internal/protocol"
)

func main() {
	out := "web/src/protocol.gen.ts"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := os.WriteFile(out, []byte(protocol.GenerateTypeScript()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", out)
}
