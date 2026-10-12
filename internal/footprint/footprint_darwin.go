// Package footprint reads the VM process's memory footprint on the Mac
// ("barely status"/"barely top") and keeps a short
// history so both commands can show the current value alongside its recent
// peak.
package footprint

/*
#include <libproc.h>
#include <sys/resource.h>

// The rusage_info_t parameter type reads like a pointer-to-pointer, but
// the kernel treats it as a plain pointer to the struct: cast &ri
// directly, don't go through an intermediate rusage_info_t (void*)
// variable.
static int barely_pid_rusage(int pid, uint64_t *phys_footprint) {
	struct rusage_info_v4 ri;
	int rc = proc_pid_rusage(pid, RUSAGE_INFO_V4, (rusage_info_t *)&ri);
	if (rc != 0) {
		return rc;
	}
	*phys_footprint = ri.ri_phys_footprint;
	return 0;
}
*/
import "C"

import "fmt"

// Read returns the phys_footprint (bytes of real RAM charged to pid): the
// VM process's phys_footprint is the guest's memory on the Mac.
func Read(pid int) (uint64, error) {
	var phys C.uint64_t
	if rc := C.barely_pid_rusage(C.int(pid), &phys); rc != 0 {
		return 0, fmt.Errorf("footprint: proc_pid_rusage(%d): rc %d", pid, rc)
	}
	return uint64(phys), nil
}
