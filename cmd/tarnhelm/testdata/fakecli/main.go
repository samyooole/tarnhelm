// fakecli is a test helper executed through tarnhelm by the integration tests.
//
//	fakecli verify EXPECTED ACTUAL   exit 0 and echo ACTUAL to stdout and stderr if equal, else exit 9
//	fakecli env                      print the environment
//	fakecli flood BYTES              write BYTES bytes to stdout
//	fakecli exit CODE                exit with CODE
package main

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "verify":
		if os.Args[2] != os.Args[3] {
			fmt.Fprintln(os.Stderr, "fakecli: argument mismatch")
			os.Exit(9)
		}
		fmt.Printf("stdout got: %s\n", os.Args[3])
		fmt.Fprintf(os.Stderr, "stderr got: %s\n", os.Args[3])
	case "env":
		for _, e := range os.Environ() {
			fmt.Println(e)
		}
	case "flood":
		n, _ := strconv.Atoi(os.Args[2])
		chunk := bytes.Repeat([]byte("x"), 64<<10)
		for n > 0 {
			k := min(n, len(chunk))
			os.Stdout.Write(chunk[:k])
			n -= k
		}
	case "exit":
		code, _ := strconv.Atoi(os.Args[2])
		fmt.Println("exiting")
		os.Exit(code)
	}
}
