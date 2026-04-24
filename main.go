package main

import (
	"log"
	"os"

	"github.com/eva/agent/cmd/eva"
)

func main() {
	if err := eva.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}