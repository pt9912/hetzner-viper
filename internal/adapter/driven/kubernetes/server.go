package kubernetes

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// EnsureLoginKey gives the key itself as its reference: the cluster keeps no key objects, the key goes with the
// machine (into its Secret).
func (*Client) EnsureLoginKey(_ context.Context, _, publicKey string) (model.LoginKey, error) {
	return model.LoginKey{Ref: strings.TrimSpace(publicKey)}, nil
}

// EnsureFirewall does nothing and says the rules are complete: a pod is reached through the API of the cluster, so
// there is no port to open. SSH_ALLOWED_CIDRS does not apply (the machine is not on a network that can reach it).
func (*Client) EnsureFirewall(context.Context, string, []string) (model.FirewallResult, error) {
	return model.FirewallResult{Complete: true}, nil
}

// ours reports whether viper made the object.
func ours(m metav1.ObjectMeta) bool { return m.Labels[labelManaged] == "true" }

// statefulSet returns the StatefulSet of that name, nil if there is none; ErrNotOurs if viper did not make it.
func (c *Client) statefulSet(ctx context.Context, name string) (*appsv1.StatefulSet, error) {
	cs, err := c.client()
	if err != nil {
		return nil, err
	}
	sts, err := cs.AppsV1().StatefulSets(c.opts.namespace()).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !ours(sts.ObjectMeta) {
		return nil, fmt.Errorf("%w: StatefulSet %s in %s", ErrNotOurs, name, c.opts.namespace())
	}
	return sts, nil
}

func idOf(sts *appsv1.StatefulSet) string {
	if sts.UID != "" {
		return string(sts.UID)
	}
	return sts.Name // only where the API gives no UID (the fake clientset of the tests)
}

// podOf returns the pod of the server, nil if there is none yet.
func (c *Client) podOf(ctx context.Context, name string) (*corev1.Pod, error) {
	cs, err := c.client()
	if err != nil {
		return nil, err
	}
	p, err := cs.CoreV1().Pods(c.opts.namespace()).Get(ctx, podName(name), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return p, err
}

func podReady(p *corev1.Pod) bool {
	if p == nil {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func serverOf(sts *appsv1.StatefulSet, pod *corev1.Pod) *model.Server {
	s := &model.Server{ID: idOf(sts), Name: sts.Name, Created: sts.CreationTimestamp.Time, SSHPort: SSHPort, ViaAPI: true}
	if pod != nil {
		s.PrivateIP = pod.Status.PodIP
	}
	return s
}

// ServerByName returns the server of that name, nil if there is none.
func (c *Client) ServerByName(ctx context.Context, name string) (*model.Server, error) {
	sts, err := c.statefulSet(ctx, name)
	if err != nil || sts == nil {
		return nil, err
	}
	pod, err := c.podOf(ctx, name)
	if err != nil {
		return nil, err
	}
	return serverOf(sts, pod), nil
}

// ensureNamespace makes the namespace if it is not there, with a label that says viper made it (and, for the Docker
// sidecar, the Pod Security level it needs). An existing namespace is left as it is. viper never deletes it.
func (c *Client) ensureNamespace(ctx context.Context) error {
	cs, err := c.client()
	if err != nil {
		return err
	}
	ns := c.opts.namespace()
	if _, err := cs.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	labels := map[string]string{labelManaged: "true"}
	if c.opts.docker() == DockerDind {
		labels["pod-security.kubernetes.io/enforce"] = "privileged"
	}
	_, err = cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns, Labels: labels}}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// CreateServer makes the Secret, the volume and the StatefulSet and waits until the pod is ready.
func (c *Client) CreateServer(ctx context.Context, s model.ServerSpec) (*model.Server, error) {
	cs, err := c.client()
	if err != nil {
		return nil, err
	}
	if c.opts.Image == "" {
		return nil, fmt.Errorf("K8S_IMAGE is not set: the image of the machine (make image builds it)")
	}
	res, err := resourcesOf(s.Size)
	if err != nil {
		return nil, err
	}
	if len(s.Bootstrap.AuthorizedKeys) == 0 || s.Bootstrap.HostKey == "" || s.Bootstrap.HostCertificate == "" {
		return nil, fmt.Errorf("the machine has no keys to start with (ServerSpec.Bootstrap is empty)")
	}
	if err := c.ensureNamespace(ctx); err != nil {
		return nil, &cloud.OpError{Op: "making the namespace " + c.opts.namespace(), Write: true, Err: err}
	}
	if err := c.dropStale(ctx, s.Name); err != nil {
		return nil, err
	}
	sts, err := c.createObjects(ctx, cs, s, res)
	if err != nil {
		return nil, err
	}
	var pod *corev1.Pod
	err = c.wait(ctx, "the pod "+podName(s.Name)+" to be ready (pulling the image can take minutes)", func() (bool, error) {
		p, err := c.podOf(ctx, s.Name)
		if err != nil {
			return false, err
		}
		pod = p
		if bad := podFailure(p); bad != "" {
			return false, fmt.Errorf("the pod %s cannot start: %s", podName(s.Name), bad)
		}
		return podReady(p), nil
	})
	if err != nil {
		return nil, err
	}
	return serverOf(sts, pod), nil
}

// createObjects makes the Secret, the volume and the StatefulSet, in this order (the pod needs the first two).
func (c *Client) createObjects(ctx context.Context, cs *session, s model.ServerSpec, res corev1.ResourceRequirements) (*appsv1.StatefulSet, error) {
	ns := c.opts.namespace()
	if _, err := cs.CoreV1().Secrets(ns).Create(ctx, secretFor(ns, s), metav1.CreateOptions{}); err != nil {
		return nil, &cloud.OpError{Op: "creating the secret " + secretName(s.Name), Write: true, Err: err}
	}
	pvc, err := pvcFor(ns, s, c.opts)
	if err != nil {
		return nil, err
	}
	if _, err := cs.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
		return nil, &cloud.OpError{Op: "creating the volume " + pvcName(s.Name), Write: true, Err: err}
	}
	sts, err := cs.AppsV1().StatefulSets(ns).Create(ctx, statefulSetFor(ns, s, c.opts, res), metav1.CreateOptions{})
	if err != nil {
		return nil, &cloud.OpError{Op: "creating the StatefulSet " + s.Name, Write: true, Err: err}
	}
	return sts, nil
}

// podFailure says why a pod will not start, for the reasons that do not pass by waiting ("" if there is none).
func podFailure(p *corev1.Pod) string {
	if p == nil {
		return ""
	}
	for _, cs := range p.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil {
			switch w.Reason {
			case "ErrImagePull", "ImagePullBackOff", "InvalidImageName", "CreateContainerConfigError":
				return cs.Name + ": " + w.Reason + " " + w.Message
			}
		}
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == "Unschedulable" {
			return "Unschedulable: " + c.Message
		}
	}
	return ""
}

// dropStale removes the Secret and the volume that a server of that name left (a down that stopped half way), so
// that a new server does not start on the data of the old one. Only what viper made, and only when no StatefulSet
// of that name exists.
func (c *Client) dropStale(ctx context.Context, name string) error {
	cs, err := c.client()
	if err != nil {
		return err
	}
	ns := c.opts.namespace()
	if sts, err := c.statefulSet(ctx, name); err != nil || sts != nil {
		return err
	}
	if sec, err := cs.CoreV1().Secrets(ns).Get(ctx, secretName(name), metav1.GetOptions{}); err == nil && ours(sec.ObjectMeta) {
		if err := cs.CoreV1().Secrets(ns).Delete(ctx, sec.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return c.deleteVolume(ctx, name)
}

// deleteVolume deletes the volume claim of the server if viper made it, and waits until it is gone.
func (c *Client) deleteVolume(ctx context.Context, name string) error {
	cs, err := c.client()
	if err != nil {
		return err
	}
	ns := c.opts.namespace()
	p, err := cs.CoreV1().PersistentVolumeClaims(ns).Get(ctx, pvcName(name), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !ours(p.ObjectMeta) {
		return fmt.Errorf("%w: volume %s in %s", ErrNotOurs, p.Name, ns)
	}
	if err := cs.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, p.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return c.wait(ctx, "the volume "+p.Name+" to be gone", func() (bool, error) {
		_, err := cs.CoreV1().PersistentVolumeClaims(ns).Get(ctx, p.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
}

// find returns the StatefulSet with that ID (its UID), or with that name if no UID matches; nil if there is none.
func (c *Client) find(ctx context.Context, id string) (*appsv1.StatefulSet, error) {
	cs, err := c.client()
	if err != nil {
		return nil, err
	}
	list, err := cs.AppsV1().StatefulSets(c.opts.namespace()).List(ctx, metav1.ListOptions{LabelSelector: labelManaged + "=true"})
	if err != nil {
		return nil, err
	}
	for i := range list.Items {
		if idOf(&list.Items[i]) == id || list.Items[i].Name == id {
			return &list.Items[i], nil
		}
	}
	return nil, nil
}

// DeleteServer deletes the StatefulSet with its pod, the Secret and the volume, and waits until they are gone. The
// volume goes with the server: the data of the machine is destroyed, as with a VM.
func (c *Client) DeleteServer(ctx context.Context, id string) error {
	cs, err := c.client()
	if err != nil {
		return err
	}
	sts, err := c.find(ctx, id)
	if err != nil || sts == nil {
		return err // nothing of that ID: a server that is gone is no error
	}
	ns, name := c.opts.namespace(), sts.Name
	fg := metav1.DeletePropagationForeground
	if err := cs.AppsV1().StatefulSets(ns).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &fg}); err != nil && !apierrors.IsNotFound(err) {
		return &cloud.OpError{Op: "deleting the StatefulSet " + name, Write: true, Err: err}
	}
	err = c.wait(ctx, "the StatefulSet "+name+" and its pod to be gone", func() (bool, error) {
		if s, err := c.statefulSet(ctx, name); err != nil || s != nil {
			return false, err
		}
		p, err := c.podOf(ctx, name)
		return p == nil, err
	})
	if err != nil {
		return err
	}
	if err := cs.CoreV1().Secrets(ns).Delete(ctx, secretName(name), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return &cloud.OpError{Op: "deleting the secret " + secretName(name), Write: true, Err: err}
	}
	return c.deleteVolume(ctx, name)
}
