package common

// ContainsFinalizer checks if the given finalizer `f` is in the
// given list of finalizers
func ContainsFinalizer(slice []string, f string) bool {
	for _, item := range slice {
		if item == f {
			return true
		}
	}
	return false
}

// RemoveFinalizer removes the given finalizer `f` from the
// given list of finalizers
func RemoveFinalizer(slice []string, f string) (result []string) {
	for _, item := range slice {
		if item == f {
			continue
		}
		result = append(result, item)
	}
	return
}
