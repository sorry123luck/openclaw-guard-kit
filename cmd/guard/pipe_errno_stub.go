//go:build !windows

package main

func isPipeFileNotFoundErrno(error) bool {
	return false
}
