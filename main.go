// terraform-provider-iothub manages the Azure IoT Hub data plane
// (identity registry, twins, configurations, jobs) that the azurerm
// provider does not cover. See CONCEPT.md for the design.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/mwalser/terraform-provider-iothub/internal/provider"
)

// version and commit are set by goreleaser at build time
// (-X main.version=… -X main.commit=…); "dev" / "" for local builds.
var (
	version = "dev"
	commit  = ""
)

func main() {
	var debug bool

	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/mwalser/iothub",
		Debug:   debug,
	}
	if debug && commit != "" {
		log.Printf("terraform-provider-iothub %s (%s)", version, commit)
	}

	if err := providerserver.Serve(context.Background(), provider.New(version), opts); err != nil {
		log.Fatal(err.Error())
	}
}
