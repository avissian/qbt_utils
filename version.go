package main

import "fmt"

const notSet string = "not set"

// these information will be collected when build, by `-ldflags "-X main.appVersion=0.1"`
var (
	appVersion = notSet
	buildTime  = notSet
)

func printVersion() {
	fmt.Printf("Version:    %s\n", appVersion)
	fmt.Printf("Build Time: %s\n", buildTime)
}
