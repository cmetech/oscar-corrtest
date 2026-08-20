package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/releasearchive"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--list" {
		names, err := releasearchive.ListZip(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, name := range names {
			fmt.Println(name)
		}
		return
	}
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: package-zip <output.zip> <staged-root> <source-date-epoch>\n       package-zip --list <archive.zip>")
		os.Exit(2)
	}
	epoch, err := strconv.ParseInt(os.Args[3], 10, 64)
	if err != nil || epoch < 0 {
		fmt.Fprintln(os.Stderr, "source-date-epoch must be a non-negative integer")
		os.Exit(2)
	}
	if err := releasearchive.WriteZip(os.Args[1], os.Args[2], time.Unix(epoch, 0).UTC()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
