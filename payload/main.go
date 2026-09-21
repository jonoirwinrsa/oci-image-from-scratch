package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	fmt.Println("hello from an image built without docker")
	fmt.Printf("pid=%d %s/%s args=%v\n", os.Getpid(), runtime.GOOS, runtime.GOARCH, os.Args)
}
