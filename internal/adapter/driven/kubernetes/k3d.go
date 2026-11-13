package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/pt9912/hetzner-viper/internal/filelock"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// A k3d cluster cannot pull an image that exists only in the Docker of this computer, and every node of it pulls what it
// needs for itself. With K3D_REGISTRY viper up makes a registry (k3d registry create), pushes the image into it and lets the
// pods pull it from there: no download from the internet per node, and the image survives the cluster. The cluster has to
// use the registry from its start (k3d cluster create --registry-use); viper makes it that way if K3D_CLUSTER_CREATE=1 and
// it is missing. A cluster that does not use the registry gets the image with k3d image import instead.

var (
	_ cloud.ImagePreparer  = (*Client)(nil)
	_ cloud.Infrastructure = (*Client)(nil)
)

const k3dRegistryPort = "5000" // inside the Docker network; the nodes pull k3d-<name>:5000/...

// k3dClusterRe is the name of a k3d cluster.
var k3dClusterRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// k3dCluster is the k3d cluster of the context (k3d-<cluster>), "" if there is no registry setting, the context is no k3d
// context (a K3D_REGISTRY exported in the shell does not matter to a machine in another cluster) or the name cannot be a
// cluster's (settingsfile refuses it earlier).
func (o Options) k3dCluster() string {
	if o.K3DRegistry == "" || !strings.HasPrefix(o.Context, "k3d-") || !k3dClusterRe.MatchString(strings.TrimPrefix(o.Context, "k3d-")) {
		return ""
	}
	return strings.TrimPrefix(o.Context, "k3d-")
}

func (o Options) registryContainer() string { return "k3d-" + o.K3DRegistry }

// tool runs docker or k3d and words a failure with what the program said.
func (c *Client) tool(ctx context.Context, name string, args ...string) (string, error) {
	if c.opts.Run == nil {
		return "", errors.New("no way to run " + name)
	}
	out, errOut, err := c.opts.Run(ctx, "", name, args...)
	if programMissing(err) {
		return out, notInstalledError{fmt.Sprintf("%s is not installed or not on the PATH%s", name, map[string]string{"k3d": ": viper up needs it to make the k3d registry and cluster (install it, for example brew install k3d, or make them yourself: k3d registry create, k3d cluster create --registry-use)"}[name])}
	}
	if err != nil {
		msg := strings.TrimSpace(errOut)
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("%s %s: %s", name, strings.Join(args[:min(len(args), 2)], " "), msg)
	}
	return out, nil
}

// notInstalledError is the failure of a program that is not there, as tool words it.
type notInstalledError struct{ msg string }

func (e notInstalledError) Error() string { return e.msg }

// programMissing says whether the program could not be started because it is not there (what exec says, or what tool made of it).
func programMissing(err error) bool {
	var ni notInstalledError
	return err != nil && (errors.As(err, &ni) || errors.Is(err, exec.ErrNotFound) || strings.Contains(err.Error(), "executable file not found"))
}

// notThere says whether docker answered that the object does not exist. Any other failure (the daemon is down, docker is
// not on the path, no permission) is not an answer about the object: taking it for "not there" would let viper down offer
// to clean up a machine that still runs.
func notThere(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "no such object") || strings.Contains(m, "no such container")
}

// objectID is the ID of a docker container, whether it exists, and an error if docker could not say.
func (c *Client) objectID(ctx context.Context, name string) (id string, exists bool, err error) {
	out, err := c.tool(ctx, "docker", "inspect", "--format", "{{.Id}}", "--", name)
	switch {
	case err == nil:
		return strings.TrimSpace(out), true, nil
	case notThere(err):
		return "", false, nil
	}
	return "", false, err
}

// registryPort is the port the registry has on this computer, "" if it is not running.
func (c *Client) registryPort(ctx context.Context) string {
	out, err := c.tool(ctx, "docker", "port", c.opts.registryContainer(), k3dRegistryPort+"/tcp")
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	i := strings.LastIndex(line, ":")
	if err != nil || i < 0 || line[i+1:] == "" {
		return ""
	}
	return line[i+1:]
}

// ensureRegistry returns the port of the registry on this computer and whether it was made now. A missing registry is made (k3d
// picks a free port); a stopped one is started. What is made is marked before it is made, so that an interrupted make is
// still known as viper's.
func (c *Client) ensureRegistry(ctx context.Context, out io.Writer) (port string, created bool, err error) {
	if p := c.registryPort(ctx); p != "" {
		return p, false, nil
	}
	if _, err := c.tool(ctx, "docker", "start", c.opts.registryContainer()); err == nil { // it exists but was stopped
		if p := c.registryPort(ctx); p != "" {
			fmt.Fprintf(out, "The k3d registry %s was stopped; it is running again.\n", c.opts.registryContainer())
			return p, false, nil
		}
	}
	fmt.Fprintf(out, "The k3d registry %s does not exist: making it ...\n", c.opts.registryContainer())
	c.markPending("registry", c.opts.K3DRegistry)
	if _, err := c.tool(ctx, "k3d", "registry", "create", c.opts.K3DRegistry); err != nil {
		c.dropMarkerIfGone(ctx, "registry", c.opts.K3DRegistry, c.opts.registryContainer())
		return "", false, err
	}
	c.confirmMade(ctx, "registry", c.opts.K3DRegistry, c.opts.registryContainer())
	if p := c.registryPort(ctx); p != "" {
		return p, true, nil
	}
	return "", true, fmt.Errorf("the k3d registry %s has no port on this computer", c.opts.registryContainer())
}

func (c *Client) clusterNode() string { return "k3d-" + c.opts.k3dCluster() + "-server-0" }

// clusterExists says whether the cluster is there; an error if docker could not say.
func (c *Client) clusterExists(ctx context.Context) (bool, error) {
	_, exists, err := c.objectID(ctx, c.clusterNode())
	return exists, err
}

// clusterUsesRegistry reads the registries.yaml k3d wrote into the node when the cluster was made with --registry-use.
func (c *Client) clusterUsesRegistry(ctx context.Context) bool {
	out, err := c.tool(ctx, "docker", "exec", c.clusterNode(), "cat", "/etc/rancher/k3s/registries.yaml")
	return err == nil && strings.Contains(out, c.opts.registryContainer()+":"+k3dRegistryPort)
}

func (c *Client) imageLocal(ctx context.Context, image string) bool {
	_, err := c.tool(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", "--", image)
	return err == nil
}

// PrepareImage makes the registry (and the cluster, if it is asked for) and puts the image where the nodes pull it. It
// does nothing without K3D_REGISTRY or for a context that is not k3d's, and it runs once. It leaves a mark that this machine
// is about to be made, which keeps a viper down of the last other machine from deleting the cluster under it; CreateServer
// removes the mark.
func (c *Client) PrepareImage(ctx context.Context, out io.Writer) error {
	cluster := c.opts.k3dCluster()
	c.mu.Lock()
	defer c.mu.Unlock()
	if cluster == "" || c.prepared {
		return nil
	}
	if c.opts.Image == "" {
		return errors.New("K8S_IMAGE is not set: the image of the machine")
	}
	if err := os.MkdirAll(c.opts.StateDir, 0o700); err != nil {
		return err
	}
	// the machines of a group start at the same time and share the registry, the cluster and the image
	unlock, err := filelock.Lock(filepath.Join(c.opts.StateDir, c.opts.registryContainer()+".lock"))
	if err != nil {
		return err
	}
	defer unlock()
	c.source = c.opts.Image
	c.pruneMarkers(ctx, cluster)
	port, registryMade, err := c.ensureRegistry(ctx, out)
	if err != nil {
		return err
	}
	if err := c.ensureCluster(ctx, cluster, out); err != nil {
		if exists, xerr := c.clusterExists(ctx); registryMade && xerr == nil && !exists { // a registry made for a cluster that was not made would stay for nobody
			c.dropRegistry(ctx)
		}
		return err
	}
	if !c.imageLocal(ctx, c.source) {
		fmt.Fprintf(out, "The image %s is not in Docker: pulling it (several GB) ...\n", c.source)
		if _, err := c.tool(ctx, "docker", "pull", "--", c.source); err != nil {
			return fmt.Errorf("%w (an image that was built here is made with viper image build)", err)
		}
	}
	if err := c.deliver(ctx, cluster, port, out); err != nil {
		return err
	}
	c.takeLease(cluster)
	c.prepared = true
	return nil
}

// ensureCluster makes sure the cluster is there; it makes it only if K3D_CLUSTER_CREATE says so.
func (c *Client) ensureCluster(ctx context.Context, cluster string, out io.Writer) error {
	exists, err := c.clusterExists(ctx)
	if err != nil || exists {
		return err
	}
	if !c.opts.K3DCreateCluster {
		return fmt.Errorf("the k3d cluster %s does not exist: make it with k3d cluster create %s --registry-use %s:%s, or set K3D_CLUSTER_CREATE=1 and viper up makes it", cluster, cluster, c.opts.registryContainer(), k3dRegistryPort)
	}
	args := []string{"cluster", "create", cluster, "--registry-use", c.opts.registryContainer() + ":" + k3dRegistryPort, "--wait"}
	if c.opts.K3DAgents > 0 {
		args = append(args, "--agents", fmt.Sprint(c.opts.K3DAgents))
	}
	fmt.Fprintf(out, "The k3d cluster %s does not exist: making it with the registry (%d agent nodes) ...\n", cluster, c.opts.K3DAgents)
	c.markPending("cluster", cluster) // before: a make that is interrupted or times out leaves a cluster that is viper's
	if _, err := c.tool(ctx, "k3d", args...); err != nil {
		c.dropMarkerIfGone(ctx, "cluster", cluster, c.clusterNode())
		return err
	}
	c.confirmMade(ctx, "cluster", cluster, c.clusterNode())
	return nil
}

// dropRegistry deletes a registry that this run made, when the cluster that was to use it could not be made.
func (c *Client) dropRegistry(ctx context.Context) {
	if _, err := c.tool(ctx, "k3d", "registry", "delete", c.opts.registryContainer()); err == nil {
		_ = os.Remove(c.madeFile("registry", c.opts.K3DRegistry))
	}
}

// imageName is the name the image gets in the registry: its last path element, without the registry host. A reference
// with a digest (name@sha256:...) gets a tag made of the start of the digest, as a tag cannot hold the whole reference.
func imageName(source string) string {
	name, digest, ok := strings.Cut(source, "@")
	if !ok {
		return path.Base(source)
	}
	_, hex, _ := strings.Cut(digest, ":")
	return path.Base(strings.SplitN(name, ":", 2)[0]) + ":sha-" + hex[:min(len(hex), 12)]
}

// deliver puts the image where the nodes of the cluster get it: into the registry if the cluster uses it, else straight
// into the nodes.
func (c *Client) deliver(ctx context.Context, cluster, port string, out io.Writer) error {
	if !c.clusterUsesRegistry(ctx) {
		fmt.Fprintf(out, "The k3d cluster %s was made without --registry-use %s:%s, and a cluster cannot get that later: the image goes into its nodes with k3d image import (every node, no registry). To use the registry, make the cluster again.\n", cluster, c.opts.registryContainer(), k3dRegistryPort)
		if _, err := c.tool(ctx, "k3d", "image", "import", "-c", cluster, c.source); err != nil {
			return err
		}
		c.opts.Image = c.source
		return nil
	}
	name := imageName(c.source)
	pushed := "localhost:" + port + "/" + name
	for _, args := range [][]string{{"tag", "--", c.source, pushed}, {"push", "--", pushed}} {
		if _, err := c.tool(ctx, "docker", args...); err != nil {
			return err
		}
	}
	c.opts.Image = c.opts.registryContainer() + ":" + k3dRegistryPort + "/" + name
	fmt.Fprintf(out, "The image is in the k3d registry %s; the pods pull %s.\n", c.opts.registryContainer(), c.opts.Image)
	return nil
}

// What viper made is marked by a file in the state directory, and only that is removed with the last machine: a cluster or a
// registry that was there before is the user's. The file holds the ID of the docker container that viper made, so that a
// cluster or registry of the same name that the user made later (after deleting viper's by hand) is not taken for viper's.
// While it is being made, the file is empty.
func (c *Client) madeFile(kind, name string) string {
	return filepath.Join(c.opts.StateDir, "k3d-"+kind+"-"+name+".made")
}

func (c *Client) markPending(kind, name string) { _ = os.WriteFile(c.madeFile(kind, name), nil, 0o600) }

// confirmMade records the ID of the container that was made.
func (c *Client) confirmMade(ctx context.Context, kind, name, container string) {
	if id, ok, _ := c.objectID(ctx, container); ok {
		_ = os.WriteFile(c.madeFile(kind, name), []byte(id+"\n"), 0o600)
	}
}

// dropMarkerIfGone removes the mark of an object whose make failed and that is not there.
func (c *Client) dropMarkerIfGone(ctx context.Context, kind, name, container string) {
	if _, ok, err := c.objectID(ctx, container); err == nil && !ok {
		_ = os.Remove(c.madeFile(kind, name))
	}
}

// made says whether the container is one viper made: it has a mark, and the mark either is still empty (it was being made)
// or holds the ID of this very container.
func (c *Client) made(ctx context.Context, kind, name, container string) bool {
	b, err := os.ReadFile(c.madeFile(kind, name))
	if err != nil {
		return false
	}
	id, ok, err := c.objectID(ctx, container)
	if err != nil || !ok {
		return false
	}
	want := strings.TrimSpace(string(b))
	return want == "" || want == id
}

// pruneMarkers removes the marks that no longer fit: viper's container was deleted and another one has its name now.
func (c *Client) pruneMarkers(ctx context.Context, cluster string) {
	for _, m := range []struct{ kind, name, container string }{{"cluster", cluster, c.clusterNode()}, {"registry", c.opts.K3DRegistry, c.opts.registryContainer()}} {
		if _, err := os.Stat(c.madeFile(m.kind, m.name)); err != nil || c.made(ctx, m.kind, m.name, m.container) {
			continue
		}
		if _, _, err := c.objectID(ctx, m.container); err == nil { // docker answered: gone, or another's, so not viper's any more
			_ = os.Remove(c.madeFile(m.kind, m.name))
		}
	}
}

// A machine that is about to be made takes a lease, a file with the time. Without it, a viper down of the last other machine
// could delete the cluster between PrepareImage and the creation of the StatefulSet. A lease that is not given back
// (viper was killed) counts for half an hour.
const leaseTTL = 30 * time.Minute

func (c *Client) leaseFile(cluster string) string {
	return filepath.Join(c.opts.StateDir, fmt.Sprintf("k3d-lease-%s-%d-%d", cluster, os.Getpid(), time.Now().UnixNano()))
}

func (c *Client) takeLease(cluster string) {
	f := c.leaseFile(cluster)
	if os.WriteFile(f, nil, 0o600) == nil {
		c.lease = f
	}
}

// releaseLease gives the lease back: the machine exists now (or was not made) and the StatefulSet tells.
func (c *Client) releaseLease() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lease != "" {
		_ = os.Remove(c.lease)
		c.lease = ""
	}
}

// leases counts the machines that are about to be made in the cluster.
func (c *Client) leases(cluster string) int {
	files, _ := filepath.Glob(filepath.Join(c.opts.StateDir, "k3d-lease-"+cluster+"-*"))
	n := 0
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && time.Since(st.ModTime()) < leaseTTL {
			n++
		}
	}
	return n
}

// machinesLeft counts the machines viper made in the cluster, in every namespace, and those about to be made, not counting
// the one named in the namespace of this client.
func (c *Client) machinesLeft(ctx context.Context, except string) (int, error) {
	cs, err := c.client()
	if err != nil {
		return 0, err
	}
	list, err := cs.AppsV1().StatefulSets(metav1.NamespaceAll).List(ctx, metav1.ListOptions{LabelSelector: labelManaged + "=true"})
	if err != nil {
		return 0, err
	}
	n := c.leases(c.opts.k3dCluster())
	for i := range list.Items {
		if it := &list.Items[i]; it.Name != except || it.Namespace != c.opts.namespace() {
			n++
		}
	}
	return n, nil
}

// registryUsedByOthers is whether a cluster other than this one is attached to the registry: the registry is on the network
// of every cluster that uses it (k3d-<cluster>), besides the bridge.
func (c *Client) registryUsedByOthers(ctx context.Context, cluster string) bool {
	out, err := c.tool(ctx, "docker", "inspect", "--format", "{{range $name, $_ := .NetworkSettings.Networks}}{{$name}} {{end}}", "--", c.opts.registryContainer())
	if err != nil {
		return true // cannot tell: leave it
	}
	for _, n := range strings.Fields(out) {
		if strings.HasPrefix(n, "k3d-") && n != "k3d-"+cluster {
			return true
		}
	}
	return false
}

// Keys of the infrastructure items.
const (
	infraCluster  = "cluster"
	infraRegistry = "registry"
)

// InfrastructureLeft names the cluster and the registry that viper made and that go with the last machine of the cluster.
// The cluster goes with it; the registry is the user's to decide (it holds the image for other clusters). A registry is
// offered only when the cluster that uses it is gone or goes: one that a cluster still uses stays. If docker cannot say
// what is there, nothing is offered.
func (c *Client) InfrastructureLeft(ctx context.Context, machine string) []cloud.InfrastructureItem {
	cluster := c.opts.k3dCluster()
	if cluster == "" {
		return nil
	}
	exists, err := c.clusterExists(ctx)
	if err != nil {
		return nil
	}
	var items []cloud.InfrastructureItem
	if exists {
		if !c.made(ctx, "cluster", cluster, c.clusterNode()) {
			return nil
		}
		if n, err := c.machinesLeft(ctx, machine); err != nil || n > 0 {
			return nil
		}
		items = append(items, cloud.InfrastructureItem{Key: infraCluster, What: "the k3d cluster " + cluster})
	}
	if c.made(ctx, "registry", c.opts.K3DRegistry, c.opts.registryContainer()) && c.registryPort(ctx) != "" && !c.registryUsedByOthers(ctx, cluster) {
		items = append(items, cloud.InfrastructureItem{Key: infraRegistry, What: "the k3d registry " + c.opts.registryContainer(), Optional: true})
	}
	return items
}

// RemoveInfrastructure takes the lock of the registry, which the machines that are deleted at the same time share, looks at
// what is left and lets choose pick the items (by key) to remove: the cluster, if viper made it and no machine of viper is
// left in it, and the registry, if viper made it and no other cluster uses it. choose is not called if nothing is left.
func (c *Client) RemoveInfrastructure(ctx context.Context, out io.Writer, choose func([]cloud.InfrastructureItem) []string) error {
	cluster := c.opts.k3dCluster()
	if cluster == "" {
		return nil
	}
	if err := os.MkdirAll(c.opts.StateDir, 0o700); err != nil {
		return err
	}
	unlock, err := filelock.Lock(filepath.Join(c.opts.StateDir, c.opts.registryContainer()+".lock"))
	if err != nil {
		return err
	}
	defer unlock()
	items := c.InfrastructureLeft(ctx, "")
	if len(items) == 0 {
		return nil
	}
	keys := choose(items)
	for _, it := range items {
		if it.Optional && !slices.Contains(keys, it.Key) { // the user keeps it: it is theirs from now on, and nobody asks again
			_ = os.Remove(c.madeFile("registry", c.opts.K3DRegistry))
			fmt.Fprintf(out, "%s stays: it is yours now (viper does not ask again; k3d registry delete %s removes it).\n", capitalize(it.What), c.opts.registryContainer())
		}
	}
	if slices.Contains(keys, infraCluster) {
		if err := c.removeCluster(ctx, cluster, out); err != nil {
			return err
		}
	}
	if slices.Contains(keys, infraRegistry) {
		if err := c.removeRegistry(ctx, cluster, out); err != nil {
			return err
		}
	}
	return nil
}

// removeCluster deletes the cluster if viper made it and it is there.
func (c *Client) removeCluster(ctx context.Context, cluster string, out io.Writer) error {
	if !c.made(ctx, "cluster", cluster, c.clusterNode()) {
		return nil
	}
	if _, err := c.tool(ctx, "k3d", "cluster", "delete", cluster); err != nil {
		return err
	}
	_ = os.Remove(c.madeFile("cluster", cluster))
	fmt.Fprintf(out, "The k3d cluster %s, which viper made, is deleted (last machine).\n", cluster)
	return nil
}

// removeRegistry deletes the registry if viper made it and no other cluster uses it.
func (c *Client) removeRegistry(ctx context.Context, cluster string, out io.Writer) error {
	if !c.made(ctx, "registry", c.opts.K3DRegistry, c.opts.registryContainer()) || c.registryUsedByOthers(ctx, cluster) {
		return nil
	}
	if _, err := c.tool(ctx, "k3d", "registry", "delete", c.opts.registryContainer()); err != nil {
		return err
	}
	_ = os.Remove(c.madeFile("registry", c.opts.K3DRegistry))
	fmt.Fprintf(out, "The k3d registry %s, which viper made, is deleted.\n", c.opts.registryContainer())
	return nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
