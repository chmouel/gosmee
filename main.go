package main

import (
	"fmt"
	"os"

	gosmee "github.com/chmouel/gosmee/gosmee"
	"github.com/mgutz/ansi"
)

func main() {
	if err := gosmee.Run(os.Args); err != nil {
		os.Stdout.WriteString(fmt.Sprintf("%s gosmee %s\n", ansi.Color("ERROR", "red+b"), err.Error()))
	}
}
