package main

import (
	"testing"

	"github.com/nicholas-cecere/terraform-provider-litellm/internal/metadata"
)

func TestServeOptionsUsePublishedAddressAndPreserveDebug(t *testing.T) {
	t.Parallel()

	for _, debug := range []bool{false, true} {
		opts := serveOptions(debug)
		if opts.Address != metadata.ProviderSource {
			t.Fatalf("Address = %q, want metadata source %q", opts.Address, metadata.ProviderSource)
		}
		if opts.Debug != debug {
			t.Fatalf("Debug = %t, want %t", opts.Debug, debug)
		}
	}
}
