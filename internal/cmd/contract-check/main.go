package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nicholas-cecere/terraform-provider-litellm/internal/contractapi"
)

func main() {
	extract := flag.Bool("extract", false, "print the provider HTTP operation inventory as JSON")
	root := flag.String("root", ".", "provider repository root")
	flag.Parse()

	absolute, err := filepath.Abs(*root)
	if err != nil {
		fatal(err)
	}
	if *extract {
		raw, err := contractapi.ExtractProvider(filepath.Join(absolute, "internal/provider"))
		if err != nil {
			fatal(err)
		}
		contracts, _, _, err := contractapi.LoadContracts(filepath.Join(absolute, "openapi.json"), filepath.Join(absolute, "internal/contract/supplemental-routes.json"))
		if err != nil {
			fatal(err)
		}
		operations, err := contractapi.ResolveOperations(raw, contracts)
		if err != nil {
			fatal(err)
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(operations); err != nil {
			fatal(err)
		}
		return
	}
	if err := contractapi.Verify(absolute); err != nil {
		fatal(err)
	}
	fmt.Println("LiteLLM API contract is current")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "contract check failed:", err)
	os.Exit(1)
}
