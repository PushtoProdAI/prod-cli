package main

import (
	"log"

	"github.com/conduitio/ecdysis"
	"github.com/meroxa/prod/cli/cmd/root"
)

func main() {
	e := ecdysis.New()
	cmd := e.MustBuildCobraCommand(&root.RootCommand{})
	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
