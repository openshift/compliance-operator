package utils

import (
	"os"
	"syscall"
)

func NewDirectory(path string, info os.FileInfo) Directory {
	//nolint:errcheck
	statinfo := info.Sys().(*syscall.Stat_t)
	return Directory{
		CreationTime: timespecToTime(statinfo.Mtim),
		Path:         path,
	}
}
