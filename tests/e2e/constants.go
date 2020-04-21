package e2e

import "time"

var (
	retryInterval        = time.Second * 5
	timeout              = time.Minute * 15
	cleanupRetryInterval = time.Second * 1
	cleanupTimeout       = time.Minute * 5
	mcoWaitTimeout       = time.Minute * 30
)
