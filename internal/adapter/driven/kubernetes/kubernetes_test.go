package kubernetes_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/kubernetes"
	"github.com/pt9912/hetzner-viper/internal/model"
)

// cluster is a fake cluster that behaves like a real one in the two ways the adapter relies on: a StatefulSet makes
// its pod, and deleting it takes the pod away.
type cluster struct {
	cs       *fake.Clientset
	notReady bool // the pod never gets ready
	pullErr  bool // the image cannot be pulled
}

func newCluster() *cluster {
	c := &cluster{cs: fake.NewSimpleClientset()}
	c.cs.PrependReactor("create", "statefulsets", func(a k8stesting.Action) (bool, runtime.Object, error) {
		sts := a.(k8stesting.CreateAction).GetObject().(*appsv1.StatefulSet)
		sts.UID = types.UID("uid-" + sts.Name)
		sts.CreationTimestamp = metav1.NewTime(time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC))
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: sts.Name + "-0", Namespace: sts.Namespace, Labels: sts.Spec.Template.Labels}}
		pod.Status.PodIP = "10.42.0.7"
		switch {
		case c.pullErr:
			pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "machine", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "not found"}}}}
		case !c.notReady:
			pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
		}
		_, _ = c.cs.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), sts.Namespace, pod.Name)
		_ = c.cs.Tracker().Add(pod)
		return false, nil, nil // go on: the clientset stores the StatefulSet
	})
	c.cs.PrependReactor("delete", "statefulsets", func(a k8stesting.Action) (bool, runtime.Object, error) {
		d := a.(k8stesting.DeleteAction)
		_ = c.cs.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), d.GetNamespace(), d.GetName()+"-0")
		return false, nil, nil
	})
	c.cs.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		r := a.(k8stesting.CreateAction).GetObject().(*authv1.SelfSubjectAccessReview)
		r.Status.Allowed = true
		return true, r, nil
	})
	return c
}

func client(c *cluster, o kubernetes.Options) *kubernetes.Client {
	o.Poll, o.Timeout = time.Millisecond, 200*time.Millisecond
	if o.Image == "" {
		o.Image = "viper-machine:test"
	}
	return kubernetes.NewWith(o, c.cs)
}

func spec(name string) model.ServerSpec {
	return model.ServerSpec{
		Name: name, Size: "2x4", Image: "ignored", Labels: map[string]string{model.LabelNATSGroup: "dev"},
		Bootstrap: model.Bootstrap{AuthorizedKeys: []string{"ssh-ed25519 AAAA login", "ssh-ed25519 BBBB ipad"}, HostKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nxx\n-----END OPENSSH PRIVATE KEY-----", HostCertificate: "ssh-ed25519-cert-v01@openssh.com AAAA host"},
	}
}

func TestCreateMakesSecretVolumeAndStatefulSetAndWaitsForThePod(t *testing.T) {
	c := newCluster()
	k := client(c, kubernetes.Options{Namespace: "w"})
	ctx := context.Background()
	srv, err := k.CreateServer(ctx, spec("claude-alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if srv.ID != "uid-claude-alpha" || srv.Name != "claude-alpha" || srv.IPv4 != "" || !srv.ViaAPI || srv.SSHPort != 2222 || srv.PrivateIP != "10.42.0.7" {
		t.Errorf("%+v", srv)
	}
	ns, _ := c.cs.CoreV1().Namespaces().Get(ctx, "w", metav1.GetOptions{})
	if ns.Labels["viper-managed"] != "true" || ns.Labels["pod-security.kubernetes.io/enforce"] != "privileged" {
		t.Errorf("the namespace viper made says so, and allows the Docker sidecar: %v", ns.Labels)
	}
	sec, err := c.cs.CoreV1().Secrets("w").Get(ctx, "claude-alpha-ssh", metav1.GetOptions{})
	if err != nil || sec.StringData["authorized_keys"] != "ssh-ed25519 AAAA login\nssh-ed25519 BBBB ipad\n" || !strings.HasSuffix(sec.StringData["host_key"], "-----END OPENSSH PRIVATE KEY-----\n") {
		t.Errorf("%+v %v", sec, err)
	}
	pvc, err := c.cs.CoreV1().PersistentVolumeClaims("w").Get(ctx, "claude-alpha-home", metav1.GetOptions{})
	if err != nil || pvc.Spec.Resources.Requests.Storage().String() != "20Gi" || pvc.Labels["viper-managed"] != "true" {
		t.Errorf("%+v %v", pvc, err)
	}
	brew, err := c.cs.CoreV1().PersistentVolumeClaims("w").Get(ctx, "claude-alpha-brew", metav1.GetOptions{})
	if err != nil || brew.Spec.Resources.Requests.Storage().String() != "10Gi" || brew.Labels["viper-managed"] != "true" {
		t.Errorf("a volume of its own for Homebrew, so that what brew installs survives a new pod: %+v %v", brew, err)
	}
	sts, err := c.cs.AppsV1().StatefulSets("w").Get(ctx, "claude-alpha", metav1.GetOptions{})
	if err != nil || *sts.Spec.Replicas != 1 || sts.Labels[model.LabelNATSGroup] != "dev" {
		t.Fatalf("%+v %v", sts, err)
	}
	mounts := map[string]string{}
	for _, m := range sts.Spec.Template.Spec.Containers[0].VolumeMounts {
		mounts[m.Name] = m.MountPath
	}
	if mounts["home"] != "/home/builder" || mounts["brew"] != "/home/linuxbrew" {
		t.Errorf("%v", mounts)
	}
	if got, err := k.ServerByName(ctx, "claude-alpha"); err != nil || got == nil || got.ID != srv.ID {
		t.Errorf("%+v %v", got, err)
	}
	if none, err := k.ServerByName(ctx, "claude-other"); err != nil || none != nil {
		t.Errorf("%+v %v", none, err)
	}
}

func TestThePodRunsWithoutRootAndWithoutKeysInTheEnvironment(t *testing.T) {
	c := newCluster()
	k := client(c, kubernetes.Options{Namespace: "w"})
	if _, err := k.CreateServer(context.Background(), spec("claude-alpha")); err != nil {
		t.Fatal(err)
	}
	sts, _ := c.cs.AppsV1().StatefulSets("w").Get(context.Background(), "claude-alpha", metav1.GetOptions{})
	pod := sts.Spec.Template.Spec
	m := pod.Containers[0]
	sc := m.SecurityContext
	if m.Name != "machine" || !*sc.RunAsNonRoot || *sc.RunAsUser != 1000 || *sc.AllowPrivilegeEscalation || sc.Capabilities.Drop[0] != "ALL" || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("the machine container: %+v", sc)
	}
	if *pod.AutomountServiceAccountToken {
		t.Error("the machine has no use for the API of the cluster")
	}
	for _, e := range m.Env {
		if strings.Contains(e.Value, "PRIVATE KEY") || strings.Contains(e.Value, "ssh-ed25519") {
			t.Errorf("a key in the environment: %s", e.Name)
		}
	}
	for _, a := range append(m.Command, m.Args...) {
		if strings.Contains(a, "ssh-ed25519") {
			t.Error("a key in the command line")
		}
	}
	if len(pod.Containers) != 2 || pod.Containers[1].Name != "dockerd" || !*pod.Containers[1].SecurityContext.Privileged {
		t.Errorf("the Docker sidecar (the default): %+v", pod.Containers)
	}
	if m.Env[0].Name != "DOCKER_HOST" || m.Env[0].Value != "tcp://127.0.0.1:2375" || pod.Containers[1].ReadinessProbe == nil {
		t.Errorf("the machine finds the daemon, and the pod is ready when the daemon is: %+v", m.Env)
	}
	if m.Resources.Limits.Cpu().String() != "2" || m.Resources.Limits.Memory().String() != "4Gi" || m.Resources.Requests.Cpu().String() != "2" {
		t.Errorf("size 2x4: %+v", m.Resources)
	}
}

func TestDockerModes(t *testing.T) {
	for _, tc := range []struct {
		mode, host string
		containers int
		env        string
	}{{"dind", "", 2, "tcp://127.0.0.1:2375"}, {"remote", "tcp://build:2375", 1, "tcp://build:2375"}, {"none", "", 1, ""}} {
		c := newCluster()
		k := client(c, kubernetes.Options{Namespace: "w", Docker: tc.mode, DockerHost: tc.host})
		if _, err := k.CreateServer(context.Background(), spec("claude-a")); err != nil {
			t.Fatal(err)
		}
		sts, _ := c.cs.AppsV1().StatefulSets("w").Get(context.Background(), "claude-a", metav1.GetOptions{})
		got := ""
		if e := sts.Spec.Template.Spec.Containers[0].Env; len(e) > 0 {
			got = e[0].Value
		}
		if len(sts.Spec.Template.Spec.Containers) != tc.containers || got != tc.env {
			t.Errorf("%s: %d containers, DOCKER_HOST %q", tc.mode, len(sts.Spec.Template.Spec.Containers), got)
		}
		ns, _ := c.cs.CoreV1().Namespaces().Get(context.Background(), "w", metav1.GetOptions{})
		if (ns.Labels["pod-security.kubernetes.io/enforce"] == "privileged") != (tc.mode == "dind") {
			t.Errorf("%s: namespace labels %v", tc.mode, ns.Labels)
		}
	}
}

func TestAPodThatCannotStartEndsTheWaitWithTheReason(t *testing.T) {
	c := newCluster()
	c.pullErr = true
	_, err := client(c, kubernetes.Options{Namespace: "w"}).CreateServer(context.Background(), spec("claude-a"))
	if err == nil || !strings.Contains(err.Error(), "ImagePullBackOff") {
		t.Errorf("%v", err)
	}
	c = newCluster()
	c.notReady = true
	_, err = client(c, kubernetes.Options{Namespace: "w"}).CreateServer(context.Background(), spec("claude-a"))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("%v", err)
	}
}

func TestCreateRefusesWhatIsMissing(t *testing.T) {
	c := newCluster()
	k := client(c, kubernetes.Options{Namespace: "w"})
	s := spec("claude-a")
	s.Size = "big"
	if _, err := k.CreateServer(context.Background(), s); err == nil || !strings.Contains(err.Error(), "<cpus>x<GiB") {
		t.Errorf("a size that is none: %v", err)
	}
	s = spec("claude-a")
	s.Bootstrap = model.Bootstrap{}
	if _, err := k.CreateServer(context.Background(), s); err == nil {
		t.Error("no keys")
	}
	noImage := kubernetes.NewWith(kubernetes.Options{Namespace: "w"}, c.cs)
	if _, err := noImage.CreateServer(context.Background(), spec("claude-a")); err == nil || !strings.Contains(err.Error(), "K8S_IMAGE") {
		t.Errorf("no image: %v", err)
	}
	if got, _ := c.cs.AppsV1().StatefulSets("w").List(context.Background(), metav1.ListOptions{}); len(got.Items) != 0 {
		t.Error("nothing is made before the checks pass")
	}
}

func TestDeleteTakesEverythingTheServerHadAndOnlyThat(t *testing.T) {
	c := newCluster()
	k := client(c, kubernetes.Options{Namespace: "w"})
	ctx := context.Background()
	a, err := k.CreateServer(ctx, spec("claude-a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.CreateServer(ctx, spec("claude-b")); err != nil {
		t.Fatal(err)
	}
	if err := k.DeleteServer(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude-a"} {
		if _, err := c.cs.AppsV1().StatefulSets("w").Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Errorf("StatefulSet %s: %v", name, err)
		}
		if _, err := c.cs.CoreV1().Secrets("w").Get(ctx, name+"-ssh", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Errorf("Secret: %v", err)
		}
		for _, v := range []string{"-home", "-brew"} {
			if _, err := c.cs.CoreV1().PersistentVolumeClaims("w").Get(ctx, name+v, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Errorf("volume %s: %v", v, err)
			}
		}
	}
	for _, v := range []string{"claude-b-home", "claude-b-brew"} {
		if _, err := c.cs.CoreV1().PersistentVolumeClaims("w").Get(ctx, v, metav1.GetOptions{}); err != nil {
			t.Errorf("the other server's volume %s is not touched: %v", v, err)
		}
	}
	if _, err := c.cs.CoreV1().Namespaces().Get(ctx, "w", metav1.GetOptions{}); err != nil {
		t.Errorf("the namespace stays: %v", err)
	}
	if err := k.DeleteServer(ctx, a.ID); err != nil {
		t.Errorf("a server that is gone is no error: %v", err)
	}
}

func TestSomethingViperDidNotMakeIsLeftAlone(t *testing.T) {
	c := newCluster()
	ctx := context.Background()
	_, _ = c.cs.AppsV1().StatefulSets("w").Create(ctx, &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "claude-a", Namespace: "w"}}, metav1.CreateOptions{})
	k := client(c, kubernetes.Options{Namespace: "w"})
	if _, err := k.ServerByName(ctx, "claude-a"); !errors.Is(err, kubernetes.ErrNotOurs) {
		t.Errorf("a StatefulSet of that name that is not ours: %v", err)
	}
	if _, err := k.CreateServer(ctx, spec("claude-a")); !errors.Is(err, kubernetes.ErrNotOurs) {
		t.Errorf("%v", err)
	}
}

func TestAVolumeLeftByAHalfDoneDownIsNotInheritedByTheNextServer(t *testing.T) {
	c := newCluster()
	ctx := context.Background()
	for _, o := range []metav1.ObjectMeta{{Name: "claude-a-home", Namespace: "w", Labels: map[string]string{"viper-managed": "true"}, UID: "old"}} {
		_, _ = c.cs.CoreV1().PersistentVolumeClaims("w").Create(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: o}, metav1.CreateOptions{})
	}
	k := client(c, kubernetes.Options{Namespace: "w"})
	if _, err := k.CreateServer(ctx, spec("claude-a")); err != nil {
		t.Fatal(err)
	}
	p, err := c.cs.CoreV1().PersistentVolumeClaims("w").Get(ctx, "claude-a-home", metav1.GetOptions{})
	if err != nil || p.UID == "old" {
		t.Errorf("a new volume, not the old one: %+v %v", p, err)
	}
	// a volume of that name that is not ours is never deleted
	c2 := newCluster()
	_, _ = c2.cs.CoreV1().PersistentVolumeClaims("w").Create(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "claude-a-home", Namespace: "w"}}, metav1.CreateOptions{})
	if _, err := client(c2, kubernetes.Options{Namespace: "w"}).CreateServer(ctx, spec("claude-a")); !errors.Is(err, kubernetes.ErrNotOurs) {
		t.Errorf("%v", err)
	}
}

func TestLoginKeyAndFirewallNeedNothingInACluster(t *testing.T) {
	k := client(newCluster(), kubernetes.Options{})
	key, err := k.EnsureLoginKey(context.Background(), "x", " ssh-ed25519 AAAA k\n")
	if err != nil || key.Ref != "ssh-ed25519 AAAA k" {
		t.Errorf("%+v %v", key, err)
	}
	fw, err := k.EnsureFirewall(context.Background(), "x", []string{"0.0.0.0/0"})
	if err != nil || !fw.Complete || fw.Created {
		t.Errorf("%+v %v", fw, err)
	}
}

func texts(fs []model.Finding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString([]string{"ok ", "info ", "WARN ", "FAIL "}[f.Level] + f.Text + "\n")
	}
	return b.String()
}

func TestValidateReadsTheClusterAndNamesWhatIsWrong(t *testing.T) {
	c := newCluster()
	_, _ = c.cs.StorageV1().StorageClasses().Create(context.Background(), &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "local-path", Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}}}, metav1.CreateOptions{})
	k := client(c, kubernetes.Options{Namespace: "w", Context: "k3d-x"})
	ch := model.Check{ServerName: "claude-a", Spec: model.ServerSpec{Size: "2x4"}}
	got := texts(k.Validate(context.Background(), ch))
	for _, want := range []string{"ok cluster k3d-x answers", "ok size 2x4", "ok image viper-machine:test", "info namespace w will be made", "ok the login may make and delete StatefulSets", "ok default storage class local-path", "WARN K8S_DOCKER=dind runs a privileged", "info server claude-a will be created"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	// a namespace that does not allow the Docker sidecar, a login without rights, no storage class, a bad size
	c2 := newCluster()
	_, _ = c2.cs.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "w", Labels: map[string]string{"pod-security.kubernetes.io/enforce": "restricted"}}}, metav1.CreateOptions{})
	c2.cs.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		r := a.(k8stesting.CreateAction).GetObject().(*authv1.SelfSubjectAccessReview)
		r.Status.Allowed = r.Spec.ResourceAttributes.Resource != "secrets"
		return true, r, nil
	})
	bad := texts(client(c2, kubernetes.Options{Namespace: "w"}).Validate(context.Background(), model.Check{ServerName: "claude-a", Spec: model.ServerSpec{Size: "huge"}}))
	for _, want := range []string{`FAIL namespace w enforces Pod Security "restricted"`, "FAIL the login may not, in namespace w: [create secrets delete secrets]", "FAIL the cluster has no default storage class", "FAIL SERVER_TYPE for Kubernetes is <cpus>x<GiB"} {
		if !strings.Contains(bad, want) {
			t.Errorf("missing %q in\n%s", want, bad)
		}
	}
	// no context: the current one of the kubeconfig is never taken
	noCtx := texts(kubernetes.New(kubernetes.Options{}).Validate(context.Background(), ch))
	if !strings.Contains(noCtx, "FAIL Kubernetes API not usable") || !strings.Contains(noCtx, "SERVER_LOCATION is not set") {
		t.Errorf("%s", noCtx)
	}
}

func TestAPullSecretIsNamedInThePodAndCheckedByThePreflight(t *testing.T) {
	c := newCluster()
	ctx := context.Background()
	k := client(c, kubernetes.Options{Namespace: "w", Context: "k3d-x", PullSecret: "regcred"})
	ch := model.Check{ServerName: "claude-a", Spec: model.ServerSpec{Size: "2x4"}}
	if got := texts(k.Validate(ctx, ch)); !strings.Contains(got, "FAIL K8S_IMAGE_PULL_SECRET: the Secret regcred is not in the namespace w") || !strings.Contains(got, "docker-registry regcred") {
		t.Errorf("a missing Secret, with the command to make it:\n%s", got)
	}
	_, _ = c.cs.CoreV1().Secrets("w").Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "regcred", Namespace: "w"}, Type: corev1.SecretTypeOpaque}, metav1.CreateOptions{})
	if got := texts(k.Validate(ctx, ch)); !strings.Contains(got, "FAIL K8S_IMAGE_PULL_SECRET: the Secret regcred is of type \"Opaque\"") {
		t.Errorf("a Secret that is no registry login:\n%s", got)
	}
	_ = c.cs.CoreV1().Secrets("w").Delete(ctx, "regcred", metav1.DeleteOptions{})
	_, _ = c.cs.CoreV1().Secrets("w").Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "regcred", Namespace: "w"}, Type: corev1.SecretTypeDockerConfigJson}, metav1.CreateOptions{})
	if got := texts(k.Validate(ctx, ch)); !strings.Contains(got, "ok the Secret regcred logs the cluster in to the registry") {
		t.Errorf("%s", got)
	}
	if _, err := k.CreateServer(ctx, spec("claude-a")); err != nil {
		t.Fatal(err)
	}
	sts, _ := c.cs.AppsV1().StatefulSets("w").Get(ctx, "claude-a", metav1.GetOptions{})
	if got := sts.Spec.Template.Spec.ImagePullSecrets; len(got) != 1 || got[0].Name != "regcred" {
		t.Errorf("the pod logs in with it: %v", got)
	}
	// viper does not own the Secret: deleting the server leaves it
	srv, _ := k.ServerByName(ctx, "claude-a")
	if err := k.DeleteServer(ctx, srv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.cs.CoreV1().Secrets("w").Get(ctx, "regcred", metav1.GetOptions{}); err != nil {
		t.Errorf("the Secret of the user stays: %v", err)
	}
	// without the setting the pod names none
	plain := newCluster()
	if _, err := client(plain, kubernetes.Options{Namespace: "w"}).CreateServer(ctx, spec("claude-b")); err != nil {
		t.Fatal(err)
	}
	if s, _ := plain.cs.AppsV1().StatefulSets("w").Get(ctx, "claude-b", metav1.GetOptions{}); len(s.Spec.Template.Spec.ImagePullSecrets) != 0 {
		t.Errorf("%v", s.Spec.Template.Spec.ImagePullSecrets)
	}
}
