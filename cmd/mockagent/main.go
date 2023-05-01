package main

import (
	"bufio"
	"fmt"
	"os"
)

func main() {
	f, err := os.Create("/tmp/_log_mock_agent.txt")
	if err != nil {
		panic(err)
	}

	for i, arg := range os.Args {
		fmt.Fprintln(f, "arg", i, "=", arg)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		fmt.Fprintln(f, scanner.Text())
	}
	if scanner.Err() != nil {
		panic(scanner.Err)
	}
}
