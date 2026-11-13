package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

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

// k3dCluster is the k3d cluster of the context (k3d-<cluster>), "" if there is no registry setting or the context is no k3d
// context: a K3D_REGISTRY exported in the shell does not matter to a machine in another cluster.
func (o Options) k3dCluster() string {
	if o.K3DRegistry == "" || !strings.HasPrefix(o.Context, "k3d-") {
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
	if err != nil {
		msg := strings.TrimSpace(errOut)
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("%s %s: %s", name, strings.Join(args[:min(len(args), 2)], " "), msg)
	}
	return out, nil
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

// ensureRegistry returns the port of the registry on this computer. A missing registry is made (k3d picks a free port); a
// stopped one is started.
func (c *Client) ensureRegistry(ctx context.Context, out io.Writer) (string, error) {
	if p := c.registryPort(ctx); p != "" {
		return p, nil
	}
	if _, err := c.tool(ctx, "docker", "start", c.opts.registryContainer()); err == nil { // it exists but was stopped
		if p := c.registryPort(ctx); p != "" {
			fmt.Fprintf(out, "The k3d registry %s was stopped; it is running again.\n", c.opts.registryContainer())
			return p, nil
		}
	}
	fmt.Fprintf(out, "The k3d registry %s does not exist: making it ...\n", c.opts.registryContainer())
	if _, err := c.tool(ctx, "k3d", "registry", "create", c.opts.K3DRegistry); err != nil {
		return "", err
	}
	c.markMade("registry", c.opts.K3DRegistry)
	if p := c.registryPort(ctx); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("the k3d registry %s has no port on this computer", c.opts.registryContainer())
}

func (c *Client) clusterNode() string { return "k3d-" + c.opts.k3dCluster() + "-server-0" }

func (c *Client) clusterExists(ctx context.Context) bool {
	_, err := c.tool(ctx, "docker", "inspect", "--format", "{{.Id}}", c.clusterNode())
	return err == nil
}

// clusterUsesRegistry reads the registries.yaml k3d wrote into the node when the cluster was made with --registry-use.
func (c *Client) clusterUsesRegistry(ctx context.Context) bool {
	out, err := c.tool(ctx, "docker", "exec", c.clusterNode(), "cat", "/etc/rancher/k3s/registries.yaml")
	return err == nil && strings.Contains(out, c.opts.registryContainer()+":"+k3dRegistryPort)
}

func (c *Client) imageLocal(ctx context.Context, image string) bool {
	_, err := c.tool(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", image)
	return err == nil
}

// PrepareImage makes the registry (and the cluster, if it is asked for) and puts the image where the nodes pull it. It
// does nothing without K3D_REGISTRY or for a context that is not k3d's, and it runs once.
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
	port, err := c.ensureRegistry(ctx, out)
	if err != nil {
		return err
	}
	if err := c.ensureCluster(ctx, cluster, out); err != nil {
		return err
	}
	if !c.imageLocal(ctx, c.source) {
		fmt.Fprintf(out, "The image %s is not in Docker: pulling it (several GB) ...\n", c.source)
		if _, err := c.tool(ctx, "docker", "pull", c.source); err != nil {
			return fmt.Errorf("%w (an image that was built here is made with viper image build)", err)
		}
	}
	if err := c.deliver(ctx, cluster, port, out); err != nil {
		return err
	}
	c.prepared = true
	return nil
}

// ensureCluster makes sure the cluster is there; it makes it only if K3D_CLUSTER_CREATE says so.
func (c *Client) ensureCluster(ctx context.Context, cluster string, out io.Writer) error {
	if c.clusterExists(ctx) {
		return nil
	}
	if !c.opts.K3DCreateCluster {
		return fmt.Errorf("the k3d cluster %s does not exist: make it with k3d cluster create %s --registry-use %s:%s, or set K3D_CLUSTER_CREATE=1 and viper up makes it", cluster, cluster, c.opts.registryContainer(), k3dRegistryPort)
	}
	args := []string{"cluster", "create", cluster, "--registry-use", c.opts.registryContainer() + ":" + k3dRegistryPort, "--wait"}
	if c.opts.K3DAgents > 0 {
		args = append(args, "--agents", fmt.Sprint(c.opts.K3DAgents))
	}
	fmt.Fprintf(out, "The k3d cluster %s does not exist: making it with the registry (%d agent nodes) ...\n", cluster, c.opts.K3DAgents)
	if _, err := c.tool(ctx, "k3d", args...); err != nil {
		return err
	}
	c.markMade("cluster", cluster)
	return nil
}

// deliver puts the image where the nodes of the cluster get it: into the registry if the cluster uses it, else straight
// into the nodes.
func (c *Client) deliver(ctx context.Context, cluster, port string, out io.Writer) error {
	if !c.clusterUsesRegistry(ctx) {
		fmt.Fprintf(out, "The k3d cluster %s was made without --registry-use %s:%s, and a cluster cannot get that later: the image goes into its nodes with k3d image import (every node, no registry). To use the registry, make the cluster again.\n", cluster, c.opts.registryContainer(), k3dRegistryPort)
		if _, err := c.tool(ctx, "k3d", "image", "import", c.source, "-c", cluster); err != nil {
			return err
		}
		c.opts.Image = c.source
		return nil
	}
	name := path.Base(c.source) // viper-machine:<tag>, without the registry and the path it came from
	pushed := "localhost:" + port + "/" + name
	for _, args := range [][]string{{"tag", c.source, pushed}, {"push", pushed}} {
		if _, err := c.tool(ctx, "docker", args...); err != nil {
			return err
		}
	}
	c.opts.Image = c.opts.registryContainer() + ":" + k3dRegistryPort + "/" + name
	fmt.Fprintf(out, "The image is in the k3d registry %s; the pods pull %s.\n", c.opts.registryContainer(), c.opts.Image)
	return nil
}

// What viper made is marked by a file in the state directory, and only that is removed with the last machine: a cluster or a
// registry that was there before is the user's.
func (c *Client) madeFile(kind, name string) string {
	return filepath.Join(c.opts.StateDir, "k3d-"+kind+"-"+name+".made")
}

func (c *Client) markMade(kind, name string) { _ = os.WriteFile(c.madeFile(kind, name), nil, 0o600) }

func (c *Client) made(kind, name string) bool {
	_, err := os.Stat(c.madeFile(kind, name))
	return err == nil
}

// machinesLeft counts the machines viper made in the cluster, in every namespace, not counting the one named.
func (c *Client) machinesLeft(ctx context.Context, except string) (int, error) {
	cs, err := c.client()
	if err != nil {
		return 0, err
	}
	list, err := cs.AppsV1().StatefulSets(metav1.NamespaceAll).List(ctx, metav1.ListOptions{LabelSelector: labelManaged + "=true"})
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range list.Items {
		if list.Items[i].Name != except {
			n++
		}
	}
	return n, nil
}

// registryUsedByOthers is whether a cluster other than this one is attached to the registry: the registry is on the network
// of every cluster that uses it (k3d-<cluster>), besides the bridge.
func (c *Client) registryUsedByOthers(ctx context.Context, cluster string) bool {
	out, err := c.tool(ctx, "docker", "inspect", "--format", "{{range $name, $_ := .NetworkSettings.Networks}}{{$name}} {{end}}", c.opts.registryContainer())
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
// offered only when the cluster that uses it is gone or goes: one that a cluster still uses stays.
func (c *Client) InfrastructureLeft(ctx context.Context, machine string) []cloud.InfrastructureItem {
	cluster := c.opts.k3dCluster()
	if cluster == "" {
		return nil
	}
	gone := !c.clusterExists(ctx)
	var items []cloud.InfrastructureItem
	if !gone {
		if !c.made("cluster", cluster) {
			return nil
		}
		if n, err := c.machinesLeft(ctx, machine); err != nil || n > 0 {
			return nil
		}
		items = append(items, cloud.InfrastructureItem{Key: infraCluster, What: "the k3d cluster " + cluster})
	}
	if c.made("registry", c.opts.K3DRegistry) && c.registryPort(ctx) != "" && !c.registryUsedByOthers(ctx, cluster) {
		items = append(items, cloud.InfrastructureItem{Key: infraRegistry, What: "the k3d registry " + c.opts.registryContainer(), Optional: true})
	}
	return items
}

// RemoveInfrastructure deletes what the keys name and viper made: the cluster, if no machine of viper is left in it, and the
// registry, if no other cluster uses it.
func (c *Client) RemoveInfrastructure(ctx context.Context, out io.Writer, keys []string) error {
	cluster := c.opts.k3dCluster()
	if cluster == "" || len(keys) == 0 {
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
	if slices.Contains(keys, infraCluster) && c.made("cluster", cluster) && c.clusterExists(ctx) {
		if n, err := c.machinesLeft(ctx, ""); err != nil || n > 0 {
			return err
		}
		if _, err := c.tool(ctx, "k3d", "cluster", "delete", cluster); err != nil {
			return err
		}
		_ = os.Remove(c.madeFile("cluster", cluster))
		fmt.Fprintf(out, "The k3d cluster %s, which viper made, is deleted (last machine).\n", cluster)
	}
	if slices.Contains(keys, infraRegistry) && c.made("registry", c.opts.K3DRegistry) && !c.registryUsedByOthers(ctx, cluster) {
		if _, err := c.tool(ctx, "k3d", "registry", "delete", c.opts.registryContainer()); err != nil {
			return err
		}
		_ = os.Remove(c.madeFile("registry", c.opts.K3DRegistry))
		fmt.Fprintf(out, "The k3d registry %s, which viper made, is deleted.\n", c.opts.registryContainer())
	}
	return nil
}
