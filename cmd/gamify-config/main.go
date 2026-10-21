// Command gamify-config checks a gamify.yaml and generates Centrifugo's config.json from it. It needs
// only the YAML and the secrets it references (dummy values are fine for -check), not a brand's code.
//
//	gamify-config -f gamify.yaml -o infra/centrifugo/config.json
//	gamify-config -f gamify.yaml -check
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/amir1430/gamify/config"
)

func main() {
	path := flag.String("f", "gamify.yaml", "config file")
	check := flag.Bool("check", false, "validate only")
	out := flag.String("o", "", "write Centrifugo's config.json here (default stdout)")
	flag.Parse()

	c, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch {
	case *check:
	case *out == "":
		os.Stdout.Write(c.CentrifugoJSON())
	default:
		if err := os.WriteFile(*out, c.CentrifugoJSON(), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
