//go:build !linux

package coldstore

// lowerThreadPriority does nothing where priorities are not per thread.
func lowerThreadPriority() {}
