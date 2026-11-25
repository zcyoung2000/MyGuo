//go:build ignore

// Verify all published installers, update manifests and signatures.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"myguo/internal/releaseassets"
)

func main() {
	dir := flag.String("dir", "dist/release-assets", "flat directory of release assets")
	targets := flag.String("targets", "darwin-arm64,darwin-amd64,windows-amd64", "required release targets")
	flag.Parse()
	data, err := os.ReadFile("mygo.json")
	var config releaseassets.Config
	if err == nil {
		err = json.Unmarshal(data, &config)
	}
	if err == nil {
		err = releaseassets.Verify(*dir, config, strings.Split(*targets, ","))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Verified release installers, manifests and update signatures:", *targets)
}
