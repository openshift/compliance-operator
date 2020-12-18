package e2e

import "time"

const (
	retryInterval                 = time.Second * 5
	timeout                       = time.Minute * 20
	cleanupRetryInterval          = time.Second * 1
	cleanupTimeout                = time.Minute * 5
	machineOperationTimeout       = time.Minute * 25
	machineOperationRetryInterval = time.Second * 10
	maxRetries                    = 5
	workerPoolName                = "worker"
	testPoolName                  = "e2e"
	rhcosContentFile              = "ssg-rhcos4-ds.xml"
	ocpContentFile                = "ssg-ocp4-ds.xml"
	unexistentResourceContentFile = "ocp4-unexistent-resource.xml"
)
