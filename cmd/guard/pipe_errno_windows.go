//go:build windows

package main

import (
	"errors"
	"syscall"
)

func isPipeFileNotFoundErrno(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == syscall.ERROR_FILE_NOT_FOUND
}
