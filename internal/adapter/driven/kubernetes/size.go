package kubernetes

import (
	"fmt"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var sizeRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)x(\d+(?:\.\d+)?)$`)

// resourcesOf reads SERVER_TYPE of this provider: "2x8" is 2 CPUs and 8 GiB. Requests and limits are the same, so
// the pod has the size it was asked for and no neighbour takes it away.
func resourcesOf(size string) (corev1.ResourceRequirements, error) {
	m := sizeRe.FindStringSubmatch(size)
	if m == nil {
		return corev1.ResourceRequirements{}, fmt.Errorf("SERVER_TYPE for Kubernetes is <cpus>x<GiB of memory>, like 2x8: %q", size)
	}
	cpu, err := resource.ParseQuantity(m[1])
	if err != nil || cpu.IsZero() {
		return corev1.ResourceRequirements{}, fmt.Errorf("SERVER_TYPE: %q CPUs", m[1])
	}
	mem, err := resource.ParseQuantity(m[2] + "Gi")
	if err != nil || mem.IsZero() {
		return corev1.ResourceRequirements{}, fmt.Errorf("SERVER_TYPE: %q GiB", m[2])
	}
	l := corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: mem}
	return corev1.ResourceRequirements{Requests: l, Limits: l}, nil
}
