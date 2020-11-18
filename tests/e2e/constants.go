package e2e

import "time"

const (
	// general time constants
	retryInterval = time.Second * 5
	timeout       = time.Minute * 20
	// node/node pool related time constants
	nodeTimeout      = time.Minute * 25
	nodePollInterval = time.Second * 10
	// cleanup related time constants
	cleanupRetryInterval = time.Second * 1
	cleanupTimeout       = time.Minute * 7
	workerPoolName       = "worker"
	testPoolName         = "e2e"
	rhcosContentFile     = "ssg-rhcos4-ds.xml"
	ocpContentFile       = "ssg-ocp4-ds.xml"
)
