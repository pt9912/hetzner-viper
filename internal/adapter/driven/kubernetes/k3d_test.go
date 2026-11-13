package kubernetes_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/kubernetes"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// k3dHost is a docker and a k3d that answer from a state: whether the registry runs, whether it exists but is stopped,
// whether the cluster is there and uses the registry, and whether the image is in Docker.
type k3dHost struct {
	registry, stopped, cluster, usesRegistry, imageLocal bool
	registryNets                                         string // the networks the registry is on, besides the bridge
	calls                                                []string
	clusterGen, registryGen                              int  // who made them: the ID of a container changes when it is made again
	dockerDown                                           bool // the daemon does not answer
	createFails, halfMade                                bool // k3d cluster create fails, and leaves the cluster half made
}

func (h *k3dHost) run(_ context.Context, _, name string, args ...string) (string, string, error) {
	h.calls = append(h.calls, name+" "+strings.Join(args, " "))
	no := func(what string) (string, string, error) {
		return "", "Error: No such " + what, errors.New("exit status 1")
	}
	if h.dockerDown && name == "docker" {
		return "", "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?", errors.New("exit status 1")
	}
	switch {
	case name == "docker" && args[0] == "port":
		if h.registry {
			return "0.0.0.0:5099\n[::]:5099\n", "", nil
		}
		return no("container: k3d-reg")
	case name == "docker" && args[0] == "start":
		if h.stopped {
			h.registry = true
			return "", "", nil
		}
		return no("container: k3d-reg")
	case name == "k3d" && args[0] == "registry" && args[1] == "create":
		h.registry = true
		h.registryGen++
	case name == "k3d" && args[0] == "cluster" && args[1] == "delete":
		h.cluster, h.usesRegistry = false, false
	case name == "k3d" && args[0] == "cluster":
		if h.createFails {
			if h.halfMade {
				h.cluster, h.usesRegistry = true, true
				h.clusterGen++
			}
			return "", "context deadline exceeded", errors.New("exit status 1")
		}
		h.cluster, h.usesRegistry = true, true
		h.clusterGen++
	case name == "k3d" && args[0] == "registry" && args[1] == "delete":
		h.registry = false
	case name == "docker" && args[0] == "inspect" && strings.Contains(strings.Join(args, " "), "NetworkSettings"):
		return h.registryNets, "", nil
	case name == "docker" && args[0] == "inspect" && strings.Contains(strings.Join(args, " "), "k3d-reg"):
		if !h.registry {
			return no("object: k3d-reg")
		}
		return fmt.Sprintf("registry-id-%d\n", h.registryGen), "", nil
	case name == "docker" && args[0] == "inspect":
		if !h.cluster {
			return no("object: k3d-viper-server-0")
		}
		return fmt.Sprintf("cluster-id-%d\n", h.clusterGen), "", nil
	case name == "docker" && args[0] == "exec":
		if h.usesRegistry {
			return "mirrors:\n  k3d-reg:5000:\n    endpoint:\n    - http://k3d-reg:5000\n", "", nil
		}
		return "", "", errors.New("exit status 1")
	case name == "docker" && args[0] == "image":
		if !h.imageLocal {
			return no("image")
		}
	case name == "docker" && args[0] == "pull":
		h.imageLocal = true
	}
	return "", "", nil
}

func (h *k3dHost) called(prefix string) int {
	n := 0
	for _, c := range h.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func k3dClient(t *testing.T, c *cluster, h *k3dHost, o kubernetes.Options) *kubernetes.Client {
	t.Helper()
	o.Context, o.K3DRegistry, o.Run = "k3d-viper", "reg", h.run
	if o.StateDir == "" {
		o.StateDir = t.TempDir()
	}
	if o.Namespace == "" {
		o.Namespace = "w"
	}
	o.Docker = "none"
	if o.Image == "" {
		o.Image = "ghcr.io/pt9912/viper-machine:v1"
	}
	return client(c, o)
}

// settle is what viper up does after PrepareImage: the machine is made (which gives the lease back) and, here, deleted again.
func settle(t *testing.T, k *kubernetes.Client) {
	t.Helper()
	ctx := context.Background()
	if _, err := k.CreateServer(ctx, spec("claude-settle")); err != nil {
		t.Fatal(err)
	}
	if err := k.DeleteServer(ctx, "uid-claude-settle"); err != nil {
		t.Fatal(err)
	}
}

func podImage(t *testing.T, c *cluster) string {
	t.Helper()
	sts, err := c.cs.AppsV1().StatefulSets("w").Get(context.Background(), "claude-alpha", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return sts.Spec.Template.Spec.Containers[0].Image
}

func TestViperUpMakesTheRegistryAndTheClusterAndPutsTheImageIntoTheRegistry(t *testing.T) {
	h := &k3dHost{}
	c := newCluster()
	k := k3dClient(t, c, h, kubernetes.Options{K3DCreateCluster: true, K3DAgents: 2})
	var out bytes.Buffer
	if err := k.PrepareImage(context.Background(), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, want := range []string{"k3d registry create reg", "k3d cluster create viper --registry-use k3d-reg:5000 --wait --agents 2",
		"docker pull -- ghcr.io/pt9912/viper-machine:v1", "docker tag -- ghcr.io/pt9912/viper-machine:v1 localhost:5099/viper-machine:v1", "docker push -- localhost:5099/viper-machine:v1"} {
		if h.called(want) != 1 {
			t.Errorf("missing %q in\n%s", want, strings.Join(h.calls, "\n"))
		}
	}
	if _, err := k.CreateServer(context.Background(), spec("claude-alpha")); err != nil {
		t.Fatal(err)
	}
	if got := podImage(t, c); got != "k3d-reg:5000/viper-machine:v1" {
		t.Errorf("the pod pulls %q, not from the registry", got)
	}
	// once: the second call changes nothing
	n := len(h.calls)
	if err := k.PrepareImage(context.Background(), &out); err != nil || len(h.calls) != n {
		t.Errorf("a second call did something: %v %v", err, h.calls[n:])
	}
}

func TestAnExistingClusterAndRegistryAreLeftAloneAndAStoppedRegistryIsStarted(t *testing.T) {
	h := &k3dHost{registry: true, cluster: true, usesRegistry: true, imageLocal: true}
	k := k3dClient(t, newCluster(), h, kubernetes.Options{})
	if err := k.PrepareImage(context.Background(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if h.called("k3d ") != 0 || h.called("docker pull") != 0 {
		t.Errorf("something that was there was made again: %v", h.calls)
	}
	h = &k3dHost{stopped: true, cluster: true, usesRegistry: true, imageLocal: true}
	var out bytes.Buffer
	if err := k3dClient(t, newCluster(), h, kubernetes.Options{}).PrepareImage(context.Background(), &out); err != nil || h.called("k3d registry") != 0 || !strings.Contains(out.String(), "was stopped") {
		t.Errorf("%v %v\n%s", err, h.calls, out.String())
	}
}

func TestAMissingClusterIsOnlyMadeWhenAskedFor(t *testing.T) {
	h := &k3dHost{registry: true, imageLocal: true}
	err := k3dClient(t, newCluster(), h, kubernetes.Options{}).PrepareImage(context.Background(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "K3D_CLUSTER_CREATE=1") || h.called("k3d cluster") != 0 {
		t.Fatalf("%v %v", err, h.calls)
	}
}

func TestAClusterWithoutTheRegistryGetsTheImageWithImportAndKeepsItsName(t *testing.T) {
	h := &k3dHost{registry: true, cluster: true, imageLocal: true}
	c := newCluster()
	k := k3dClient(t, c, h, kubernetes.Options{})
	var out bytes.Buffer
	if err := k.PrepareImage(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if h.called("k3d image import -c viper ghcr.io/pt9912/viper-machine:v1") != 1 || h.called("docker push") != 0 || !strings.Contains(out.String(), "make the cluster again") {
		t.Fatalf("%v\n%s", h.calls, out.String())
	}
	if _, err := k.CreateServer(context.Background(), spec("claude-alpha")); err != nil {
		t.Fatal(err)
	}
	if got := podImage(t, c); got != "ghcr.io/pt9912/viper-machine:v1" {
		t.Errorf("the imported image keeps its name: %q", got)
	}
}

func TestWithoutTheSettingOrOutsideK3dNothingIsDone(t *testing.T) {
	h := &k3dHost{}
	for name, o := range map[string]kubernetes.Options{
		"no registry":        {Context: "k3d-viper", Run: h.run},
		"another cluster":    {Context: "gke_x_y_z", K3DRegistry: "reg", Run: h.run},
		"registry and no id": {Context: "k3d-viper", K3DRegistry: "", Run: h.run},
	} {
		if err := kubernetes.NewWith(o, newCluster().cs).PrepareImage(context.Background(), &bytes.Buffer{}); err != nil || len(h.calls) != 0 {
			t.Errorf("%s: %v %v", name, err, h.calls)
		}
	}
}

func TestThePreflightSaysWhatViperUpWillDoAboutTheRegistry(t *testing.T) {
	// the cluster is missing and viper makes it: no API to ask, an Info
	h := &k3dHost{imageLocal: true}
	f := k3dClient(t, newCluster(), h, kubernetes.Options{K3DCreateCluster: true}).Validate(context.Background(), model.Check{Spec: spec("claude-alpha"), ServerName: "claude-alpha"})
	if len(f) == 0 || !strings.Contains(texts(f), "viper up makes it with the registry k3d-reg") || strings.Contains(texts(f), "Kubernetes API not usable") {
		t.Errorf("%s", texts(f))
	}
	// the cluster is missing and viper does not make it: a failure that says how
	f = k3dClient(t, newCluster(), &k3dHost{}, kubernetes.Options{}).Validate(context.Background(), model.Check{Spec: spec("claude-alpha"), ServerName: "claude-alpha"})
	if !strings.Contains(texts(f), "K3D_CLUSTER_CREATE=1") {
		t.Errorf("%s", texts(f))
	}
	// the cluster is there without the registry
	h = &k3dHost{registry: true, cluster: true, imageLocal: true}
	f = k3dClient(t, newCluster(), h, kubernetes.Options{}).Validate(context.Background(), model.Check{Spec: spec("claude-alpha"), ServerName: "claude-alpha"})
	if !strings.Contains(texts(f), "made without --registry-use k3d-reg:5000") {
		t.Errorf("%s", texts(f))
	}
}

func TestAServerOfAK3dClusterThatDoesNotExistYetIsNoServerAndNoError(t *testing.T) {
	h := &k3dHost{}
	k := k3dClient(t, newCluster(), h, kubernetes.Options{K3DCreateCluster: true})
	srv, err := k.ServerByName(context.Background(), "claude-alpha")
	if srv != nil || err != nil {
		t.Fatalf("%v %v", srv, err)
	}
	if h.called("docker inspect") != 1 {
		t.Errorf("the cluster was looked for: %v", h.calls)
	}
}

// all and only are the choices of the user: everything, or the one item.
func all(items []cloud.InfrastructureItem) []string {
	var k []string
	for _, it := range items {
		k = append(k, it.Key)
	}
	return k
}

func only(key string) func([]cloud.InfrastructureItem) []string {
	return func([]cloud.InfrastructureItem) []string { return []string{key} }
}

func keysOf(items []cloud.InfrastructureItem) string {
	var k []string
	for _, it := range items {
		k = append(k, it.Key)
	}
	return strings.Join(k, ",")
}

func TestTheClusterAndTheRegistryViperMadeGoWithTheLastMachineOnly(t *testing.T) {
	ctx := context.Background()
	h := &k3dHost{}
	c := newCluster()
	k := k3dClient(t, c, h, kubernetes.Options{K3DCreateCluster: true})
	if err := k.PrepareImage(ctx, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"claude-alpha", "claude-beta"} {
		if _, err := k.CreateServer(ctx, spec(n)); err != nil {
			t.Fatal(err)
		}
	}
	if left := k.InfrastructureLeft(ctx, "claude-alpha"); left != nil {
		t.Errorf("another machine is still there: %v", left)
	}
	if err := k.DeleteServer(ctx, "uid-claude-beta"); err != nil {
		t.Fatal(err)
	}
	if err := k.RemoveInfrastructure(ctx, &bytes.Buffer{}, all); err != nil || h.called("k3d cluster delete") != 0 {
		t.Fatalf("one machine is left, and the cluster went: %v %v", err, h.calls)
	}
	left := k.InfrastructureLeft(ctx, "claude-alpha")
	if keysOf(left) != "cluster,registry" || left[0].Optional || !left[1].Optional || !strings.Contains(left[1].What, "k3d-reg") {
		t.Fatalf("the cluster goes, the registry is the user's to decide: %+v", left)
	}
	if err := k.DeleteServer(ctx, "uid-claude-alpha"); err != nil {
		t.Fatal(err)
	}
	if keysOf(k.InfrastructureLeft(ctx, "")) != "cluster,registry" {
		t.Errorf("after the delete the same is left")
	}
	// the user keeps the registry: the cluster goes, the registry is the user's from now on and is not offered again
	var out bytes.Buffer
	if err := k.RemoveInfrastructure(ctx, &out, only("cluster")); err != nil {
		t.Fatal(err)
	}
	if h.called("k3d cluster delete viper") != 1 || h.called("k3d registry delete") != 0 ||
		!strings.Contains(out.String(), "cluster viper, which viper made, is deleted") || !strings.Contains(out.String(), "it is yours now") {
		t.Fatalf("%v\n%s", h.calls, out.String())
	}
	if got := k.InfrastructureLeft(ctx, ""); got != nil {
		t.Errorf("a registry the user keeps is offered again: %+v", got)
	}
}

func TestBothGoWhenTheUserSaysSoAndOfTwoMachinesThatEndTogetherOnlyTheFirstIsAsked(t *testing.T) {
	ctx := context.Background()
	h := &k3dHost{}
	k := k3dClient(t, newCluster(), h, kubernetes.Options{K3DCreateCluster: true})
	if err := k.PrepareImage(ctx, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	settle(t, k)
	asked := 0
	both := func(items []cloud.InfrastructureItem) []string { asked++; return all(items) }
	for i := 0; i < 2; i++ { // two machines end together
		if err := k.RemoveInfrastructure(ctx, &bytes.Buffer{}, both); err != nil {
			t.Fatal(err)
		}
	}
	if asked != 1 || h.called("k3d cluster delete viper") != 1 || h.called("k3d registry delete k3d-reg") != 1 {
		t.Errorf("asked %d times: %v", asked, h.calls)
	}
	// and when the first keeps the registry, the second is not asked about it again
	h = &k3dHost{}
	k = k3dClient(t, newCluster(), h, kubernetes.Options{K3DCreateCluster: true})
	if err := k.PrepareImage(ctx, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	settle(t, k)
	asked = 0
	keep := func([]cloud.InfrastructureItem) []string { asked++; return []string{"cluster"} }
	for i := 0; i < 2; i++ {
		if err := k.RemoveInfrastructure(ctx, &bytes.Buffer{}, keep); err != nil {
			t.Fatal(err)
		}
	}
	if asked != 1 || h.called("k3d registry delete") != 0 {
		t.Errorf("asked %d times: %v", asked, h.calls)
	}
}

func TestARegistryThatAnotherClusterUsesStaysAndWhatWasNotMadeByViperIsNeverRemoved(t *testing.T) {
	ctx := context.Background()
	h := &k3dHost{registryNets: "bridge k3d-viper k3d-other "}
	c := newCluster()
	k := k3dClient(t, c, h, kubernetes.Options{K3DCreateCluster: true})
	if err := k.PrepareImage(ctx, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	settle(t, k)
	if left := k.InfrastructureLeft(ctx, "claude-alpha"); keysOf(left) != "cluster" {
		t.Fatalf("only the cluster goes: %+v", left)
	}
	if err := k.RemoveInfrastructure(ctx, &bytes.Buffer{}, all); err != nil || h.called("k3d cluster delete viper") != 1 || h.called("k3d registry delete") != 0 {
		t.Errorf("a registry that another cluster uses stays, even if asked for: %v %v", err, h.calls)
	}

	// a cluster and a registry that were there before are the user's
	h = &k3dHost{registry: true, cluster: true, usesRegistry: true, imageLocal: true}
	k = k3dClient(t, newCluster(), h, kubernetes.Options{})
	if err := k.PrepareImage(ctx, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if left := k.InfrastructureLeft(ctx, "claude-alpha"); left != nil {
		t.Errorf("%v", left)
	}
	if err := k.RemoveInfrastructure(ctx, &bytes.Buffer{}, all); err != nil || h.called("k3d cluster delete") != 0 || h.called("k3d registry delete") != 0 {
		t.Errorf("something that viper did not make was removed: %v %v", err, h.calls)
	}
}

// --- what the review found

func TestADockerThatDoesNotAnswerIsNotAClusterThatIsGone(t *testing.T) {
	ctx := context.Background()
	h := &k3dHost{registry: true, cluster: true, usesRegistry: true, imageLocal: true}
	k := k3dClient(t, newCluster(), h, kubernetes.Options{})
	h.dockerDown = true
	if srv, err := k.ServerByName(ctx, "claude-alpha"); err == nil || srv != nil || !strings.Contains(err.Error(), "Cannot connect to the Docker daemon") {
		t.Errorf("viper down would say 'not found' and offer CLEANUP for a machine that runs: %v %v", srv, err)
	}
	if left := k.InfrastructureLeft(ctx, ""); left != nil {
		t.Errorf("nothing is offered when docker cannot say: %v", left)
	}
	f := k.Validate(ctx, model.Check{Spec: spec("claude-alpha"), ServerName: "claude-alpha"})
	if !strings.Contains(texts(f), "docker could not say whether the k3d cluster viper exists") {
		t.Errorf("%s", texts(f))
	}
	if err := k.PrepareImage(ctx, &bytes.Buffer{}); err == nil {
		t.Error("the image was prepared without knowing the cluster")
	}
}

func TestAClusterThatWasBeingMadeIsMarkedBeforeAndKeptWhenItIsThereHalf(t *testing.T) {
	ctx := context.Background()
	// the make is cut off and leaves a cluster: it is viper's and goes with the last machine
	h := &k3dHost{createFails: true, halfMade: true}
	k := k3dClient(t, newCluster(), h, kubernetes.Options{K3DCreateCluster: true})
	if err := k.PrepareImage(ctx, &bytes.Buffer{}); err == nil {
		t.Fatal("the failed make was no error")
	}
	if left := k.InfrastructureLeft(ctx, ""); keysOf(left) != "cluster,registry" {
		t.Errorf("a half made cluster of viper's leaks: %+v", left)
	}
	// the make fails and nothing is left: no mark, and the registry that was made for it goes again
	h = &k3dHost{createFails: true}
	k = k3dClient(t, newCluster(), h, kubernetes.Options{K3DCreateCluster: true})
	if err := k.PrepareImage(ctx, &bytes.Buffer{}); err == nil {
		t.Fatal("the failed make was no error")
	}
	if h.called("k3d registry delete k3d-reg") != 1 || h.registry {
		t.Errorf("the registry made for a cluster that was not made stays: %v", h.calls)
	}
	if left := k.InfrastructureLeft(ctx, ""); left != nil {
		t.Errorf("%+v", left)
	}
}

func TestAClusterOfTheSameNameThatTheUserMadeLaterIsNotVipers(t *testing.T) {
	ctx := context.Background()
	h := &k3dHost{}
	dir := t.TempDir()
	k := k3dClient(t, newCluster(), h, kubernetes.Options{K3DCreateCluster: true, StateDir: dir})
	if err := k.PrepareImage(ctx, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	// the user deletes viper's cluster by hand and makes one of their own with the same name, and the registry again
	h.clusterGen++
	h.registryGen++
	if left := k.InfrastructureLeft(ctx, ""); left != nil {
		t.Fatalf("the user's cluster and registry are offered for deletion: %+v", left)
	}
	if err := k.RemoveInfrastructure(ctx, &bytes.Buffer{}, all); err != nil || h.called("k3d cluster delete") != 0 || h.called("k3d registry delete") != 0 {
		t.Errorf("something the user made was deleted: %v %v", err, h.calls)
	}
	// and the next viper up forgets the old marks
	h.imageLocal = true
	k2 := k3dClient(t, newCluster(), h, kubernetes.Options{StateDir: dir})
	if err := k2.PrepareImage(ctx, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if left := k2.InfrastructureLeft(ctx, ""); left != nil {
		t.Errorf("%+v", left)
	}
}

func TestTheClusterIsNotDeletedUnderAMachineThatIsAboutToBeMade(t *testing.T) {
	ctx := context.Background()
	h := &k3dHost{}
	dir := t.TempDir()
	c := newCluster()
	starting := k3dClient(t, c, h, kubernetes.Options{K3DCreateCluster: true, StateDir: dir})
	if err := starting.PrepareImage(ctx, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	// another viper (same state directory) ends the last machine now: no StatefulSet exists yet, but the lease does
	ending := k3dClient(t, c, h, kubernetes.Options{StateDir: dir})
	if left := ending.InfrastructureLeft(ctx, ""); left != nil {
		t.Fatalf("a machine is about to be made, and the cluster is offered for deletion: %+v", left)
	}
	if _, err := starting.CreateServer(ctx, spec("claude-alpha")); err != nil { // gives the lease back
		t.Fatal(err)
	}
	if err := starting.DeleteServer(ctx, "uid-claude-alpha"); err != nil {
		t.Fatal(err)
	}
	if left := ending.InfrastructureLeft(ctx, ""); keysOf(left) != "cluster,registry" {
		t.Errorf("after the machine is gone and the lease given back the cluster goes: %+v", left)
	}
}

func TestAnImageWithADigestGetsATagInTheRegistry(t *testing.T) {
	h := &k3dHost{registry: true, cluster: true, usesRegistry: true, imageLocal: true}
	c := newCluster()
	k := k3dClient(t, c, h, kubernetes.Options{Image: "ghcr.io/pt9912/viper-machine@sha256:0123456789abcdef0123456789abcdef"})
	if err := k.PrepareImage(context.Background(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if h.called("docker tag -- ghcr.io/pt9912/viper-machine@sha256:0123456789abcdef0123456789abcdef localhost:5099/viper-machine:sha-0123456789ab") != 1 {
		t.Errorf("%v", h.calls)
	}
	if _, err := k.CreateServer(context.Background(), spec("claude-alpha")); err != nil {
		t.Fatal(err)
	}
	if got := podImage(t, c); got != "k3d-reg:5000/viper-machine:sha-0123456789ab" {
		t.Errorf("%q", got)
	}
}

func TestAMachineOfTheSameNameInAnotherNamespaceIsAnotherMachine(t *testing.T) {
	ctx := context.Background()
	h := &k3dHost{}
	dir := t.TempDir()
	c := newCluster()
	a := k3dClient(t, c, h, kubernetes.Options{K3DCreateCluster: true, StateDir: dir, Namespace: "a"})
	if err := a.PrepareImage(ctx, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b := k3dClient(t, c, h, kubernetes.Options{StateDir: dir, Namespace: "b"})
	for _, k := range []*kubernetes.Client{a, b} {
		if _, err := k.CreateServer(ctx, spec("claude-alpha")); err != nil {
			t.Fatal(err)
		}
	}
	if left := a.InfrastructureLeft(ctx, "claude-alpha"); left != nil {
		t.Errorf("the machine of the same name in namespace b is still there: %+v", left)
	}
}
