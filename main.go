package main

import (
	"log"
	"os"

	gosmee "github.com/chmouel/gosmee/gosmee"
)

func main() {
	if err := gosmee.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
