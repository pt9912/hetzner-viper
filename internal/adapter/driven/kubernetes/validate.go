package kubernetes

import (
	"context"
	"fmt"

	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/pt9912/hetzner-viper/internal/model"
)

type findings []model.Finding

func (f *findings) add(l model.Level, format string, a ...any) {
	*f = append(*f, model.Finding{Level: l, Text: fmt.Sprintf(format, a...)})
}

// Validate reads the cluster and says whether the server can be made: the context works, the namespace is fine, the
// login may do what viper does (asked of the cluster, nothing is changed), a storage class is there, the size and
// the image are set.
func (c *Client) Validate(ctx context.Context, ch model.Check) []model.Finding {
	var f findings
	cs, err := c.client()
	if err != nil {
		f.add(model.Fail, "Kubernetes API not usable (kubeconfig, context, login, network): %s", c.Describe(err))
		return f
	}
	v, err := cs.Discovery().ServerVersion()
	if err != nil {
		f.add(model.Fail, "Kubernetes API not usable (kubeconfig, context, login, network): %s", c.Describe(err))
		return f
	}
	f.add(model.OK, "cluster %s answers (Kubernetes %s)", c.opts.Context, v.GitVersion)
	if _, err := resourcesOf(ch.Spec.Size); err != nil {
		f.add(model.Fail, "%s", err)
	} else {
		f.add(model.OK, "size %s", ch.Spec.Size)
	}
	if c.opts.Image == "" {
		f.add(model.Fail, "K8S_IMAGE is not set: the image of the machine (viper image build builds it, with --k3d CLUSTER it puts it into a k3d cluster)")
	} else {
		f.add(model.OK, "image %s", c.opts.Image)
	}
	c.checkNamespace(ctx, cs, &f)
	c.checkPullSecret(ctx, cs, &f)
	c.checkRights(ctx, cs, &f)
	c.checkStorage(ctx, cs, &f)
	c.checkDocker(&f)
	switch srv, err := c.ServerByName(ctx, ch.ServerName); {
	case err != nil:
		f.add(model.Fail, "server %s: %s", ch.ServerName, c.Describe(err))
	case srv != nil:
		f.add(model.Info, "server %s exists; viper up reuses it", ch.ServerName)
	default:
		f.add(model.Info, "server %s will be created", ch.ServerName)
	}
	return f
}

// checkNamespace: the namespace exists, or will be made; with the Docker sidecar its Pod Security level must allow it.
func (c *Client) checkNamespace(ctx context.Context, cs kubernetes.Interface, f *findings) {
	ns := c.opts.namespace()
	n, err := cs.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		f.add(model.Info, "namespace %s will be made (viper never deletes it)", ns)
	case err != nil:
		f.add(model.Warn, "namespace %s could not be read: %s", ns, c.Describe(err))
	default:
		f.add(model.OK, "namespace %s exists", ns)
		if lvl := n.Labels["pod-security.kubernetes.io/enforce"]; c.opts.docker() == DockerDind && lvl != "" && lvl != "privileged" {
			f.add(model.Fail, "namespace %s enforces Pod Security %q, but K8S_DOCKER=dind needs a privileged container: use another namespace, set K8S_DOCKER=remote or none", ns, lvl)
		}
	}
}

// rights are what viper does in the cluster, as the questions asked of it.
func (c *Client) rights() []authv1.ResourceAttributes {
	ns := c.opts.namespace()
	return []authv1.ResourceAttributes{
		{Namespace: ns, Verb: "create", Group: "apps", Resource: "statefulsets"},
		{Namespace: ns, Verb: "delete", Group: "apps", Resource: "statefulsets"},
		{Namespace: ns, Verb: "create", Resource: "secrets"},
		{Namespace: ns, Verb: "delete", Resource: "secrets"},
		{Namespace: ns, Verb: "create", Resource: "persistentvolumeclaims"},
		{Namespace: ns, Verb: "delete", Resource: "persistentvolumeclaims"},
		{Namespace: ns, Verb: "get", Resource: "pods"},
		{Namespace: ns, Verb: "create", Resource: "pods", Subresource: "portforward"},
	}
}

// checkRights asks the cluster, with a SelfSubjectAccessReview for each thing viper does, whether the login may do it.
func (c *Client) checkRights(ctx context.Context, cs kubernetes.Interface, f *findings) {
	var denied []string
	for _, ra := range c.rights() {
		ra := ra
		r, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authv1.SelfSubjectAccessReview{Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &ra}}, metav1.CreateOptions{})
		if err != nil {
			f.add(model.Info, "the rights of the login could not be asked: %s", c.Describe(err))
			return
		}
		if !r.Status.Allowed {
			what := ra.Resource
			if ra.Subresource != "" {
				what += "/" + ra.Subresource
			}
			denied = append(denied, ra.Verb+" "+what)
		}
	}
	if len(denied) > 0 {
		f.add(model.Fail, "the login may not, in namespace %s: %v", c.opts.namespace(), denied)
		return
	}
	f.add(model.OK, "the login may make and delete StatefulSets, secrets and volumes in %s, and forward ports of pods", c.opts.namespace())
}

// checkStorage: the storage class of K8S_STORAGE_CLASS exists, or the cluster has a default one.
func (c *Client) checkStorage(ctx context.Context, cs kubernetes.Interface, f *findings) {
	if c.opts.StorageClass != "" {
		if _, err := cs.StorageV1().StorageClasses().Get(ctx, c.opts.StorageClass, metav1.GetOptions{}); err != nil {
			f.add(model.Fail, "storage class %s: %s", c.opts.StorageClass, c.Describe(err))
			return
		}
		f.add(model.OK, "storage class %s exists", c.opts.StorageClass)
		return
	}
	list, err := cs.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		f.add(model.Warn, "storage classes could not be listed: %s", c.Describe(err))
		return
	}
	for _, sc := range list.Items {
		if sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			f.add(model.OK, "default storage class %s (volume of %d GB)", sc.Name, c.opts.diskGB())
			return
		}
	}
	f.add(model.Fail, "the cluster has no default storage class: set K8S_STORAGE_CLASS")
}

func (c *Client) checkDocker(f *findings) {
	switch c.opts.docker() {
	case DockerDind:
		f.add(model.Warn, "K8S_DOCKER=dind runs a privileged Docker daemon in the pod: root on the node if it is broken into; do not use it in a shared cluster")
	case DockerRemote:
		if c.opts.DockerHost == "" {
			f.add(model.Fail, "K8S_DOCKER=remote needs K8S_DOCKER_HOST (where the Docker daemon is, like tcp://host:2375)")
		} else {
			f.add(model.OK, "Docker through %s", c.opts.DockerHost)
		}
	case DockerNone:
		f.add(model.Info, "K8S_DOCKER=none: no Docker on the machine")
	default:
		f.add(model.Fail, "K8S_DOCKER is dind, remote or none: %s", c.opts.Docker)
	}
}

// checkPullSecret: the Secret for the registry (K8S_IMAGE_PULL_SECRET) is in the namespace and is a registry login.
func (c *Client) checkPullSecret(ctx context.Context, cs kubernetes.Interface, f *findings) {
	name, ns := c.opts.PullSecret, c.opts.namespace()
	if name == "" {
		return
	}
	sec, err := cs.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		f.add(model.Fail, "K8S_IMAGE_PULL_SECRET: the Secret %s is not in the namespace %s (make the namespace and the Secret first: kubectl -n %s create secret docker-registry %s --docker-server=… --docker-username=… --docker-password=…)", name, ns, ns, name)
	case err != nil:
		f.add(model.Fail, "K8S_IMAGE_PULL_SECRET: the Secret %s could not be read: %s", name, c.Describe(err))
	case sec.Type != corev1.SecretTypeDockerConfigJson && sec.Type != corev1.SecretTypeDockercfg:
		f.add(model.Fail, "K8S_IMAGE_PULL_SECRET: the Secret %s is of type %q, not a registry login (kubernetes.io/dockerconfigjson)", name, sec.Type)
	default:
		f.add(model.OK, "the Secret %s logs the cluster in to the registry", name)
	}
}
