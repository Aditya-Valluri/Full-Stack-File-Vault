// probe checks readiness without requiring a shell in the runtime image.
package main

import (
	"net/http"
	"os"
	"time"
)

func main() {
	address := "http://127.0.0.1:8080/readyz"
	if len(os.Args) > 1 {
		address = os.Args[1]
	}
	client := http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(address)
	if err != nil {
		os.Exit(1)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		os.Exit(1)
	}
}
