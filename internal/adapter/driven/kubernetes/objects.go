package kubernetes

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/pt9912/hetzner-viper/internal/model"
)

func secretName(server string) string { return server + "-ssh" }
func pvcName(server string) string    { return server + "-home" }
func podName(server string) string    { return server + "-0" }
func brewName(server string) string   { return server + "-brew" }

// PodName is the pod of the server: the one replica of its StatefulSet.
func PodName(server string) string { return podName(server) }

func ptr[T any](v T) *T { return &v }

// labelsOf are the labels of everything viper makes for a server, plus the extra ones (the NATS labels, on the
// StatefulSet only).
func labelsOf(server string) map[string]string {
	return map[string]string{labelManaged: "true", labelServer: server}
}

func meta(ns, name, server string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labelsOf(server)}
}

// secretFor holds what the machine needs to be reachable. It is a file in the pod, never an environment variable.
func secretFor(ns string, s model.ServerSpec) *corev1.Secret {
	b := s.Bootstrap
	return &corev1.Secret{
		ObjectMeta: meta(ns, secretName(s.Name), s.Name),
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"authorized_keys":   strings.Join(b.AuthorizedKeys, "\n") + "\n",
			"host_key":          strings.TrimRight(b.HostKey, "\n") + "\n",
			"host_key.pub":      strings.TrimSpace(b.HostKeyPub) + "\n",
			"host_key-cert.pub": strings.TrimSpace(b.HostCertificate) + "\n",
		},
	}
}

// pvcFor is the volume of the home directory.
func pvcFor(ns string, s model.ServerSpec, o Options) (*corev1.PersistentVolumeClaim, error) {
	return claim(ns, s.Name, pvcName(s.Name), o.diskGB(), o)
}

// brewPVCFor is the volume of Homebrew (/home/linuxbrew): what brew installs must survive a new pod.
func brewPVCFor(ns string, s model.ServerSpec, o Options) (*corev1.PersistentVolumeClaim, error) {
	return claim(ns, s.Name, brewName(s.Name), o.brewGB(), o)
}

func claim(ns, server, name string, gb int, o Options) (*corev1.PersistentVolumeClaim, error) {
	size, err := resource.ParseQuantity(fmt.Sprintf("%dGi", gb))
	if err != nil {
		return nil, err
	}
	p := &corev1.PersistentVolumeClaim{
		ObjectMeta: meta(ns, name, server),
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: size}},
		},
	}
	if o.StorageClass != "" {
		p.Spec.StorageClassName = ptr(o.StorageClass)
	}
	return p, nil
}

// machineContainer is the machine: builder (uid 1000), no capabilities, no new privileges, so that it also starts
// where Pod Security is "restricted".
func machineSpec(o Options, res corev1.ResourceRequirements) corev1.Container {
	c := corev1.Container{
		Name: machineContainer, Image: o.Image, ImagePullPolicy: corev1.PullIfNotPresent,
		Ports:     []corev1.ContainerPort{{Name: "ssh", ContainerPort: SSHPort}},
		Resources: res,
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot: ptr(true), RunAsUser: ptr(int64(uid)), RunAsGroup: ptr(int64(uid)),
			AllowPrivilegeEscalation: ptr(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "secret", MountPath: secretDir, ReadOnly: true},
			{Name: "home", MountPath: homeDir},
			{Name: "brew", MountPath: "/home/linuxbrew"},
		},
		ReadinessProbe: tcpProbe(SSHPort),
	}
	switch o.docker() {
	case DockerDind:
		c.Env = append(c.Env, corev1.EnvVar{Name: "DOCKER_HOST", Value: fmt.Sprintf("tcp://127.0.0.1:%d", DockerPort)})
	case DockerRemote:
		c.Env = append(c.Env, corev1.EnvVar{Name: "DOCKER_HOST", Value: o.DockerHost})
	}
	return c
}

func tcpProbe(port int) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler:  corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(int32(port))}},
		PeriodSeconds: 2, FailureThreshold: 3,
	}
}

// dockerSpec is the Docker daemon next to the machine in the same pod (K8S_DOCKER=dind). It is privileged: that is
// what a Docker daemon needs in a pod (docs/plan/kubernetes.md §15), and why the namespace must allow it. The pod is
// ready only when the daemon answers.
func dockerSpec(o Options) corev1.Container {
	return corev1.Container{
		Name: dockerContainer, Image: o.dockerImage(), ImagePullPolicy: corev1.PullIfNotPresent,
		Env:             []corev1.EnvVar{{Name: "DOCKER_TLS_CERTDIR", Value: ""}},
		SecurityContext: &corev1.SecurityContext{Privileged: ptr(true)},
		VolumeMounts:    []corev1.VolumeMount{{Name: "docker", MountPath: "/var/lib/docker"}},
		ReadinessProbe:  &corev1.Probe{ProbeHandler: tcpProbe(DockerPort).ProbeHandler, PeriodSeconds: 2, FailureThreshold: 30},
	}
}

func statefulSetFor(ns string, s model.ServerSpec, o Options, res corev1.ResourceRequirements) *appsv1.StatefulSet {
	labels := labelsOf(s.Name)
	containers := []corev1.Container{machineSpec(o, res)}
	volumes := []corev1.Volume{
		{Name: "secret", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: secretName(s.Name), DefaultMode: ptr(int32(0o444))}}},
		{Name: "home", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName(s.Name)}}},
		{Name: "brew", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: brewName(s.Name)}}},
	}
	if o.docker() == DockerDind {
		containers = append(containers, dockerSpec(o))
		volumes = append(volumes, corev1.Volume{Name: "docker", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}})
	}
	sts := &appsv1.StatefulSet{
		ObjectMeta: meta(ns, s.Name, s.Name),
		Spec: appsv1.StatefulSetSpec{
			Replicas:            ptr(int32(1)),
			ServiceName:         s.Name,
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Selector:            &metav1.LabelSelector{MatchLabels: map[string]string{labelServer: s.Name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					// the machine needs no access to the API of the cluster
					AutomountServiceAccountToken:  ptr(false),
					TerminationGracePeriodSeconds: ptr(int64(10)),
					SecurityContext:               &corev1.PodSecurityContext{FSGroup: ptr(int64(uid))},
					ImagePullSecrets:              pullSecrets(o.PullSecret),
					Containers:                    containers,
					Volumes:                       volumes,
				},
			},
		},
	}
	for k, v := range s.Labels { // the NATS labels
		sts.Labels[k] = v
	}
	natsPod(sts, s.Labels)
	return sts
}

// natsPod puts what a member of a NATS group needs on the pod: the labels the NetworkPolicy and the Service select by
// (group and role, which do not change; the state is on the StatefulSet only, a change of the template would restart the
// pod), and for the server the variable POD_IP (the address it listens on) and the switch that makes the container run
// nats-server.
func natsPod(sts *appsv1.StatefulSet, labels map[string]string) {
	group, role := labels[model.LabelNATSGroup], labels[model.LabelNATSRole]
	if group == "" {
		return
	}
	sts.Spec.Template.Labels[model.LabelNATSGroup] = group
	sts.Spec.Template.Labels[model.LabelNATSRole] = role
	if role != string(model.NATSServer) {
		return
	}
	m := &sts.Spec.Template.Spec.Containers[0]
	m.Env = append(m.Env,
		corev1.EnvVar{Name: "VIPER_NATS_SERVER", Value: "1"},
		corev1.EnvVar{Name: "POD_IP", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"}}})
}

// pullSecrets names the Secret the kubelet logs in to the registry with, none if there is no setting.
func pullSecrets(name string) []corev1.LocalObjectReference {
	if name == "" {
		return nil
	}
	return []corev1.LocalObjectReference{{Name: name}}
}
