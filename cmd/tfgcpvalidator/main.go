package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	err := newRootCmd().Execute()
	if err == nil {
		return
	}

	var ec exitCodeError
	if errors.As(err, &ec) {
		os.Exit(ec.code)
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}
