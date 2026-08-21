package main

import (
	"log"
	"os"

	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

func main() {
	raw, err := scenario.GenerateJSONSchema()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("docs/schema/correlation-scenario.schema.json", raw, 0o644); err != nil {
		log.Fatal(err)
	}
}
