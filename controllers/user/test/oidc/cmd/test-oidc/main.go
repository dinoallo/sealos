package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/labring/sealos/controllers/user/test/oidc"
)

func main() {
	provider, err := oidc.NewFromJSON([]byte(os.Getenv(oidc.ConfigEnv)))
	if err != nil {
		log.Fatalf("create test OIDC provider: %v", err)
	}
	address := os.Getenv("SEALOS_TEST_OIDC_ADDR")
	if address == "" {
		address = ":8080"
	}
	server := &http.Server{Addr: address, Handler: provider}
	fmt.Printf("test OIDC provider listening on %s\n", address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
