package kubernetes

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// NATS groups in a cluster (docs/plan/kubernetes.md §6). The "private network" of a group is a NetworkPolicy
// viper-nats-<group>: it lets the NATS port in from the pods of the group to the pods of the group and from nobody else.
// Creating it is the lock of the first start: the API refuses a second create of a name (409), so only one caller becomes
// the server candidate. A headless Service of the same name points at the server pod of the group; its name is what the
// clients connect to and what the certificate of the server is for (the address of a pod changes, the name does not).
// The NATS labels are labels of the StatefulSet; group and role are also on the pod, which the policy and the Service
// select by.

var _ cloud.NATSProvider = (*Client)(nil)

const natsPrefix = "viper-nats-"

// NetworkName is the name of the NetworkPolicy and of the Service of a NATS group.
func NetworkName(group string) string { return natsPrefix + group }

func (o Options) natsPort() int {
	if o.NATSPort > 0 {
		return o.NATSPort
	}
	return model.DefaultNATSPort
}

// NetworkZone is the cluster and namespace: the machines of a group must be in one namespace of one cluster.
func (c *Client) NetworkZone(_ context.Context, location string) (string, error) {
	if location == "" {
		return "", fmt.Errorf("no cluster: set SERVER_LOCATION")
	}
	return location + "/" + c.opts.namespace(), nil
}

func (c *Client) policyFor(group string) *networkingv1.NetworkPolicy {
	proto := corev1.ProtocolTCP
	groupPods := metav1.LabelSelector{MatchLabels: map[string]string{model.LabelNATSGroup: group}}
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: NetworkName(group), Namespace: c.opts.namespace(),
			Labels: map[string]string{labelManaged: "true", model.LabelNATSGroup: group}},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: groupPods,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  []networkingv1.NetworkPolicyPeer{{PodSelector: &groupPods}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &proto, Port: ptr(intstr.FromInt32(int32(c.opts.natsPort())))}},
			}},
		},
	}
}

func (c *Client) serviceFor(group string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: NetworkName(group), Namespace: c.opts.namespace(),
			Labels: map[string]string{labelManaged: "true", model.LabelNATSGroup: group}},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone, // headless: the name resolves to the address of the (ready) server pod
			Selector:  map[string]string{model.LabelNATSGroup: group, model.LabelNATSRole: string(model.NATSServer)},
			Ports:     []corev1.ServicePort{{Name: "nats", Port: int32(c.opts.natsPort()), Protocol: corev1.ProtocolTCP}},
		},
	}
}

func (c *Client) networkOf(p *networkingv1.NetworkPolicy) *model.PrivateNetwork {
	return &model.PrivateNetwork{ID: p.Name, Name: p.Name, Zone: c.opts.Context + "/" + c.opts.namespace(), Created: p.CreationTimestamp.Time}
}

// EnsurePrivateNetwork makes the policy and the Service of the group, or finds them; see cloud.NATSProvider.
func (c *Client) EnsurePrivateNetwork(ctx context.Context, group, location string) (model.PrivateNetwork, bool, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return model.PrivateNetwork{}, false, err
	}
	cs, err := c.client()
	if err != nil {
		return model.PrivateNetwork{}, false, err
	}
	if _, err := c.NetworkZone(ctx, location); err != nil {
		return model.PrivateNetwork{}, false, err
	}
	if err := c.ensureNamespace(ctx); err != nil {
		return model.PrivateNetwork{}, false, &cloud.OpError{Op: "making the namespace " + c.opts.namespace(), Write: true, Err: err}
	}
	ns := c.opts.namespace()
	made, err := cs.NetworkingV1().NetworkPolicies(ns).Create(ctx, c.policyFor(group), metav1.CreateOptions{})
	created := err == nil
	var pol *networkingv1.NetworkPolicy
	switch {
	case created:
		pol = made
	case apierrors.IsAlreadyExists(err):
		// somebody was first: use the policy, if it is the group's
		if pol, err = cs.NetworkingV1().NetworkPolicies(ns).Get(ctx, NetworkName(group), metav1.GetOptions{}); err != nil {
			return model.PrivateNetwork{}, false, err
		}
		if !ours(pol.ObjectMeta) || pol.Labels[model.LabelNATSGroup] != group {
			return model.PrivateNetwork{}, false, fmt.Errorf("%w: NetworkPolicy %s in %s (for the NATS group %s)", ErrNotOurs, pol.Name, ns, group)
		}
	default:
		return model.PrivateNetwork{}, false, &cloud.OpError{Op: "creating the NetworkPolicy " + NetworkName(group), Write: true, Err: err}
	}
	// the Service is made by whoever comes: it is not an error if it is there, and a first start that stopped between
	// the two calls is repaired by the next
	if err := c.ensureService(ctx, cs, group); err != nil {
		return model.PrivateNetwork{}, false, err
	}
	return *c.networkOf(pol), created, nil
}

// ensureService makes the Service of the group; one that is there must be viper's.
func (c *Client) ensureService(ctx context.Context, cs *session, group string) error {
	ns := c.opts.namespace()
	_, err := cs.CoreV1().Services(ns).Create(ctx, c.serviceFor(group), metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		have, gerr := cs.CoreV1().Services(ns).Get(ctx, NetworkName(group), metav1.GetOptions{})
		if gerr != nil {
			return gerr
		}
		if !ours(have.ObjectMeta) {
			return fmt.Errorf("%w: Service %s in %s (for the NATS group %s)", ErrNotOurs, have.Name, ns, group)
		}
		return nil
	}
	if err != nil {
		return &cloud.OpError{Op: "creating the Service " + NetworkName(group), Write: true, Err: err}
	}
	return nil
}

// FindPrivateNetwork returns the policy of the group, nil if there is none; its Problems say how it differs from what
// viper makes.
func (c *Client) FindPrivateNetwork(ctx context.Context, group string) (*model.PrivateNetwork, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return nil, err
	}
	cs, err := c.client()
	if err != nil {
		return nil, err
	}
	ns := c.opts.namespace()
	pol, err := cs.NetworkingV1().NetworkPolicies(ns).Get(ctx, NetworkName(group), metav1.GetOptions{})
	if apierrors.IsNotFound(err) || (err == nil && (!ours(pol.ObjectMeta) || pol.Labels[model.LabelNATSGroup] != group)) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n := c.networkOf(pol)
	n.Problems = c.policyProblems(pol, group)
	if _, err := cs.CoreV1().Services(ns).Get(ctx, NetworkName(group), metav1.GetOptions{}); apierrors.IsNotFound(err) {
		n.Problems = append(n.Problems, "the Service "+NetworkName(group)+" is missing: the clients cannot find the server")
	} else if err != nil {
		return nil, err
	}
	return n, nil
}

// policyProblems are the ways a policy differs from what viper makes: somebody changed it.
func (c *Client) policyProblems(p *networkingv1.NetworkPolicy, group string) []string {
	want := c.policyFor(group).Spec
	var out []string
	if len(p.Spec.Ingress) != 1 || len(p.Spec.Ingress[0].Ports) != 1 || p.Spec.Ingress[0].Ports[0].Port == nil || p.Spec.Ingress[0].Ports[0].Port.IntValue() != c.opts.natsPort() {
		out = append(out, fmt.Sprintf("it does not let in exactly TCP %d", c.opts.natsPort()))
	}
	if len(p.Spec.Ingress) == 1 {
		from := p.Spec.Ingress[0].From
		if len(from) != 1 || from[0].PodSelector == nil || from[0].NamespaceSelector != nil || from[0].IPBlock != nil ||
			from[0].PodSelector.MatchLabels[model.LabelNATSGroup] != group || len(from[0].PodSelector.MatchLabels) != 1 {
			out = append(out, "it lets the port in from something other than the pods of the group")
		}
	}
	if p.Spec.PodSelector.MatchLabels[model.LabelNATSGroup] != group || len(p.Spec.PodSelector.MatchLabels) != len(want.PodSelector.MatchLabels) {
		out = append(out, "it does not apply to the pods of the group")
	}
	return out
}

// AttachPrivateNetwork puts the group label on the pod template of a machine that was made without NATS (the policy and
// the Service select by it). That starts the pod again; the volume stays. It returns the address of the new pod.
func (c *Client) AttachPrivateNetwork(ctx context.Context, serverID, networkID string) (string, error) {
	cs, err := c.client()
	if err != nil {
		return "", err
	}
	sts, err := c.find(ctx, serverID)
	if err != nil {
		return "", err
	}
	if sts == nil {
		return "", fmt.Errorf("server %s not found", serverID)
	}
	group := strings.TrimPrefix(networkID, natsPrefix)
	if sts.Spec.Template.Labels[model.LabelNATSGroup] == group {
		return c.podIP(ctx, sts.Name), nil // attached already
	}
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"labels":{%q:%q,%q:%q}}}}}`, model.LabelNATSGroup, group, model.LabelNATSRole, model.NATSClient)
	if _, err := cs.AppsV1().StatefulSets(c.opts.namespace()).Patch(ctx, sts.Name, types.MergePatchType, []byte(patch), metav1.PatchOptions{}); err != nil {
		return "", &cloud.OpError{Op: "putting the NATS group on the pod", Write: true, Err: err}
	}
	var ip string
	err = c.wait(ctx, "the pod "+podName(sts.Name)+" to be ready again with the group label", func() (bool, error) {
		p, err := c.podOf(ctx, sts.Name)
		if err != nil || p == nil || p.Labels[model.LabelNATSGroup] != group || !podReady(p) {
			return false, err
		}
		ip = p.Status.PodIP
		return true, nil
	})
	return ip, err
}

func (c *Client) podIP(ctx context.Context, name string) string {
	if p, err := c.podOf(ctx, name); err == nil && p != nil {
		return p.Status.PodIP
	}
	return ""
}

// DeletePrivateNetwork deletes the policy and the Service of the group if viper made them; ErrNetworkInUse if machines of
// the group still exist.
func (c *Client) DeletePrivateNetwork(ctx context.Context, networkID string) error {
	cs, err := c.client()
	if err != nil {
		return err
	}
	ns := c.opts.namespace()
	pol, err := cs.NetworkingV1().NetworkPolicies(ns).Get(ctx, networkID, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	group := pol.Labels[model.LabelNATSGroup]
	if !ours(pol.ObjectMeta) || networkID != NetworkName(group) {
		return fmt.Errorf("the NetworkPolicy %s was not made by viper for a NATS group: not deleting it", networkID)
	}
	members, err := c.ListGroupMachines(ctx, group)
	if err != nil {
		return err
	}
	if len(members) > 0 {
		return cloud.ErrNetworkInUse
	}
	if err := cs.NetworkingV1().NetworkPolicies(ns).Delete(ctx, networkID, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if svc, err := cs.CoreV1().Services(ns).Get(ctx, networkID, metav1.GetOptions{}); err == nil && ours(svc.ObjectMeta) {
		if err := cs.CoreV1().Services(ns).Delete(ctx, networkID, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// ListGroupMachines returns the machines of the group with their labels; one with labels that make no sense is an error.
func (c *Client) ListGroupMachines(ctx context.Context, group string) ([]model.GroupMember, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return nil, err
	}
	cs, err := c.client()
	if err != nil {
		return nil, err
	}
	list, err := cs.AppsV1().StatefulSets(c.opts.namespace()).List(ctx, metav1.ListOptions{LabelSelector: labelManaged + "=true," + model.LabelNATSGroup + "=" + group})
	if err != nil {
		return nil, err
	}
	var out []model.GroupMember
	for i := range list.Items {
		sts := &list.Items[i]
		meta, ok, err := model.NATSMetadataFromLabels(sts.Labels)
		if err != nil {
			return nil, fmt.Errorf("StatefulSet %s: %w", sts.Name, err)
		}
		if !ok || meta.Group != group {
			continue
		}
		pod, err := c.podOf(ctx, sts.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, model.GroupMember{Server: *serverOf(sts, pod), NATS: meta})
	}
	return out, nil
}

// UpdateNATSMetadata sets the NATS labels of the StatefulSet and keeps its other labels (a merge patch changes only the
// keys it names, so machines that write at the same time do not undo each other).
func (c *Client) UpdateNATSMetadata(ctx context.Context, serverID string, meta model.NATSMetadata) error {
	if err := model.CheckNATSName("NATS group", meta.Group); err != nil {
		return err
	}
	cs, err := c.client()
	if err != nil {
		return err
	}
	sts, err := c.find(ctx, serverID)
	if err != nil {
		return err
	}
	if sts == nil {
		return fmt.Errorf("server %s not found", serverID)
	}
	var b strings.Builder
	for k, v := range model.NATSLabels(meta) {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q:%q", k, v)
	}
	patch := `{"metadata":{"labels":{` + b.String() + `}}}`
	if _, err := cs.AppsV1().StatefulSets(c.opts.namespace()).Patch(ctx, sts.Name, types.MergePatchType, []byte(patch), metav1.PatchOptions{}); err != nil {
		return &cloud.OpError{Op: "setting the NATS labels", Write: true, Err: err}
	}
	return nil
}

// NATSMetadataOf returns the NATS labels of the machine, nil if it has none.
func (c *Client) NATSMetadataOf(ctx context.Context, serverID string) (*model.NATSMetadata, error) {
	sts, err := c.find(ctx, serverID)
	if err != nil {
		return nil, err
	}
	if sts == nil {
		return nil, fmt.Errorf("server %s not found", serverID)
	}
	meta, ok, err := model.NATSMetadataFromLabels(sts.Labels)
	if err != nil || !ok {
		return nil, err
	}
	return &meta, nil
}
