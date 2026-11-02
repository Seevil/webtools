//go:build ignore

package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	runtime.LockOSThread()
	if err := runGUI(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		messageBox(0, "启动失败", err.Error(), 0x10)
		os.Exit(1)
	}
}
