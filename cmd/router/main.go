package main

import (
	routercmd "github.com/wundergraph/cosmo/router/cmd"

	// Custom modules register themselves via init().
	_ "github.com/dperez4787/cosmo-router/modules/fieldauth"
	_ "github.com/dperez4787/cosmo-router/modules/requestlog"
	_ "github.com/dperez4787/cosmo-router/modules/subgraphtoken"
)

func main() {
	routercmd.Main()
}
