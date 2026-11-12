package compose

import (
	"fmt"
	"regexp"
	"strconv"
)

var sizeRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)x(\d+(?:\.\d+)?)$`)

// sizeOf reads SERVER_TYPE of this provider: "2x8" is 2 CPUs and 8 GiB. It returns the CPUs and the memory in MiB.
func sizeOf(size string) (cpus float64, memMiB int, err error) {
	m := sizeRe.FindStringSubmatch(size)
	if m == nil {
		return 0, 0, fmt.Errorf("SERVER_TYPE for Docker Compose is <cpus>x<GiB of memory>, like 2x8: %q", size)
	}
	cpus, _ = strconv.ParseFloat(m[1], 64)
	gib, _ := strconv.ParseFloat(m[2], 64)
	if cpus <= 0 || gib <= 0 {
		return 0, 0, fmt.Errorf("SERVER_TYPE: %q is not a size", size)
	}
	return cpus, int(gib * 1024), nil
}
