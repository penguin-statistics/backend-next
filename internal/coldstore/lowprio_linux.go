package coldstore

import (
	"syscall"

	"github.com/rs/zerolog/log"
)

const (
	ioprioWhoProcess = 1
	ioprioClassIdle  = 3
	ioprioClassShift = 13
)

// lowerThreadPriority gives the calling OS thread the lowest CPU priority (nice 19) and the idle I/O class. On
// Linux both apply to the thread alone, so the caller must hold it with runtime.LockOSThread and never release it.
func lowerThreadPriority() {
	tid := syscall.Gettid()
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, tid, 19); err != nil {
		log.Debug().Err(err).Msg("coldstore: cannot lower the CPU priority of the integrity check")
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOPRIO_SET, ioprioWhoProcess, uintptr(tid), ioprioClassIdle<<ioprioClassShift); errno != 0 {
		log.Debug().Err(errno).Msg("coldstore: cannot lower the I/O priority of the integrity check")
	}
}
