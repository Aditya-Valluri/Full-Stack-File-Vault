// init-storage creates private application directories on a provisioned volume.
package main

import (
	"fmt"
	"os"
)

func main() {
	for _, directory := range []string{"/data/staging", "/data/blobs"} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			fmt.Fprintln(os.Stderr, "storage initialization failed")
			os.Exit(1)
		}
	}
}
