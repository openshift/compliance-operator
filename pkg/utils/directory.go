package utils

import (
	"syscall"
	"time"
)

// Directory is a holding struct used to sort directories by time
type Directory struct {
	CreationTime time.Time
	Path         string
}

func timespecToTime(ts syscall.Timespec) time.Time {
	return time.Unix(int64(ts.Sec), int64(ts.Nsec))
}
