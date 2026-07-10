package main

import (
	routercmd "github.com/wundergraph/cosmo/router/cmd"

	// Custom modules register themselves via init(). The planned field-level
	// authorization module (centralized policy service) will be imported here.
	_ "github.com/dperez4787/cosmo-router/modules/requestlog"
)

func main() {
	routercmd.Main()
}
