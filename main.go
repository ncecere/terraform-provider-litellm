package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/nicholas-cecere/terraform-provider-litellm/internal/metadata"
	"github.com/nicholas-cecere/terraform-provider-litellm/internal/provider"
)

// version is set during the release process via -ldflags
var version string = "dev"

func main() {
	var debug bool

	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), serveOptions(debug))
	if err != nil {
		log.Fatal(err.Error())
	}
}

func serveOptions(debug bool) providerserver.ServeOpts {
	return providerserver.ServeOpts{
		Address: metadata.ProviderSource,
		Debug:   debug,
	}
}
