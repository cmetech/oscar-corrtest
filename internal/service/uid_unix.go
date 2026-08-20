//go:build !windows

package service

import (
	"os"
	"strconv"
)

func currentUserID() string { return strconv.Itoa(os.Getuid()) }
