package kubernetes_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8stesting "k8s.io/client-go/testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/kubernetes"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

var _ cloud.NATSProvider = (*kubernetes.Client)(nil)

func natsSpec(name string, role model.NATSRole) model.ServerSpec {
	s := spec(name)
	s.Labels = model.NATSLabels(model.NATSMetadata{Group: "dev", Role: role, State: model.NATSProvisioning})
	s.Network = "viper-nats-dev"
	return s
}

func TestTheFirstStartMakesThePolicyAndTheServiceOfTheGroupAndTheNextOnesFindThem(t *testing.T) {
	c := newCluster()
	a, b := client(c, kubernetes.Options{Namespace: "w", Context: "k3d-x"}), client(c, kubernetes.Options{Namespace: "w", Context: "k3d-x"})
	ctx := context.Background()
	n, created, err := a.EnsurePrivateNetwork(ctx, "dev", "k3d-x")
	if err != nil || !created || n.ID != "viper-nats-dev" || n.Zone != "k3d-x/w" {
		t.Fatalf("%+v %v %v", n, created, err)
	}
	pol, err := c.cs.NetworkingV1().NetworkPolicies("w").Get(ctx, "viper-nats-dev", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	in := pol.Spec.Ingress
	if pol.Spec.PodSelector.MatchLabels["viper-nats-group"] != "dev" || len(in) != 1 || in[0].From[0].PodSelector.MatchLabels["viper-nats-group"] != "dev" ||
		in[0].Ports[0].Port.IntValue() != 4222 || pol.Labels["viper-managed"] != "true" {
		t.Errorf("the port in from the group, to the group, and from nobody else: %+v", pol.Spec)
	}
	svc, err := c.cs.CoreV1().Services("w").Get(ctx, "viper-nats-dev", metav1.GetOptions{})
	if err != nil || svc.Spec.ClusterIP != corev1.ClusterIPNone || svc.Spec.Selector["viper-nats-role"] != "server" || svc.Spec.Selector["viper-nats-group"] != "dev" {
		t.Errorf("a headless Service for the server pod of the group: %+v %v", svc, err)
	}
	n2, created2, err := b.EnsurePrivateNetwork(ctx, "dev", "k3d-x")
	if err != nil || created2 || n2.ID != n.ID {
		t.Errorf("the second caller is not the first: %+v %v %v", n2, created2, err)
	}
	if found, err := a.FindPrivateNetwork(ctx, "dev"); err != nil || found == nil || len(found.Problems) != 0 {
		t.Errorf("%+v %v", found, err)
	}
	if none, err := a.FindPrivateNetwork(ctx, "other"); err != nil || none != nil {
		t.Errorf("%+v %v", none, err)
	}
}

func TestAGroupWithoutItsServiceIsRepairedByTheNextCaller(t *testing.T) {
	c := newCluster()
	k := client(c, kubernetes.Options{Namespace: "w", Context: "x"})
	ctx := context.Background()
	if _, _, err := k.EnsurePrivateNetwork(ctx, "dev", "x"); err != nil {
		t.Fatal(err)
	}
	_ = c.cs.CoreV1().Services("w").Delete(ctx, "viper-nats-dev", metav1.DeleteOptions{})
	if found, _ := k.FindPrivateNetwork(ctx, "dev"); found == nil || len(found.Problems) != 1 || !strings.Contains(found.Problems[0], "Service") {
		t.Errorf("a missing Service is reported: %+v", found)
	}
	if _, created, err := k.EnsurePrivateNetwork(ctx, "dev", "x"); err != nil || created {
		t.Errorf("%v %v", created, err)
	}
	if _, err := c.cs.CoreV1().Services("w").Get(ctx, "viper-nats-dev", metav1.GetOptions{}); err != nil {
		t.Errorf("the Service is made again: %v", err)
	}
}

func TestAPolicyThatViperDidNotMakeIsNotTakenAndAChangedOneIsReported(t *testing.T) {
	c := newCluster()
	ctx := context.Background()
	_, _ = c.cs.NetworkingV1().NetworkPolicies("w").Create(ctx, &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "viper-nats-dev", Namespace: "w"}}, metav1.CreateOptions{})
	k := client(c, kubernetes.Options{Namespace: "w", Context: "x"})
	if _, _, err := k.EnsurePrivateNetwork(ctx, "dev", "x"); !errors.Is(err, kubernetes.ErrNotOurs) {
		t.Errorf("%v", err)
	}
	if n, err := k.FindPrivateNetwork(ctx, "dev"); err != nil || n != nil {
		t.Errorf("a policy that is not ours is none: %+v %v", n, err)
	}
	// ours, but somebody opened it
	c2 := newCluster()
	k2 := client(c2, kubernetes.Options{Namespace: "w", Context: "x"})
	if _, _, err := k2.EnsurePrivateNetwork(ctx, "dev", "x"); err != nil {
		t.Fatal(err)
	}
	pol, _ := c2.cs.NetworkingV1().NetworkPolicies("w").Get(ctx, "viper-nats-dev", metav1.GetOptions{})
	pol.Spec.Ingress[0].Ports[0].Port = ptrIntStr(9999)
	pol.Spec.Ingress[0].From = []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0"}}}
	_, _ = c2.cs.NetworkingV1().NetworkPolicies("w").Update(ctx, pol, metav1.UpdateOptions{})
	n, _ := k2.FindPrivateNetwork(ctx, "dev")
	if n == nil || len(n.Problems) != 2 {
		t.Errorf("a changed port and a source outside the group: %+v", n)
	}
	for _, name := range []string{"a.b", "A"} {
		if _, _, err := k2.EnsurePrivateNetwork(ctx, name, "x"); err == nil {
			t.Errorf("name %q", name)
		}
	}
}

func ptrIntStr(i int32) *intstr.IntOrString { v := intstr.FromInt32(i); return &v }

func TestTheServerPodListensOnItsAddressAndTheClientPodDoesNot(t *testing.T) {
	c := newCluster()
	k := client(c, kubernetes.Options{Namespace: "w", Context: "x"})
	ctx := context.Background()
	if _, _, err := k.EnsurePrivateNetwork(ctx, "dev", "x"); err != nil {
		t.Fatal(err)
	}
	srv, err := k.CreateServer(ctx, natsSpec("claude-alpha", model.NATSServer))
	if err != nil || !srv.InNetwork() {
		t.Fatalf("%+v %v", srv, err)
	}
	cli, err := k.CreateServer(ctx, natsSpec("claude-beta", model.NATSClient))
	if err != nil || !cli.InNetwork() {
		t.Fatalf("%+v %v", cli, err)
	}
	env := func(name string) map[string]corev1.EnvVar {
		sts, _ := c.cs.AppsV1().StatefulSets("w").Get(ctx, name, metav1.GetOptions{})
		m := map[string]corev1.EnvVar{}
		for _, e := range sts.Spec.Template.Spec.Containers[0].Env {
			m[e.Name] = e
		}
		return m
	}
	if e := env("claude-alpha"); e["VIPER_NATS_SERVER"].Value != "1" || e["POD_IP"].ValueFrom == nil || e["POD_IP"].ValueFrom.FieldRef.FieldPath != "status.podIP" {
		t.Errorf("the server runs nats-server and knows the address of its pod: %v", e)
	}
	if e := env("claude-beta"); e["VIPER_NATS_SERVER"].Name != "" || e["POD_IP"].Name != "" {
		t.Errorf("a client runs no server: %v", e)
	}
	sts, _ := c.cs.AppsV1().StatefulSets("w").Get(ctx, "claude-alpha", metav1.GetOptions{})
	tl := sts.Spec.Template.Labels
	if tl["viper-nats-group"] != "dev" || tl["viper-nats-role"] != "server" || tl["viper-nats-state"] != "" {
		t.Errorf("group and role on the pod (the policy selects by them), the state not (a change of the template restarts the pod): %v", tl)
	}
	// a machine without NATS is not in the network
	plainSpec := spec("claude-plain")
	plainSpec.Labels = nil
	plain, err := k.CreateServer(ctx, plainSpec)
	if err != nil || plain.InNetwork() {
		t.Errorf("%+v %v", plain, err)
	}
}

func TestTheMetadataIsKeptOnTheStatefulSetAndMergedAndTheGroupIsListed(t *testing.T) {
	c := newCluster()
	k := client(c, kubernetes.Options{Namespace: "w", Context: "x"})
	ctx := context.Background()
	_, _, _ = k.EnsurePrivateNetwork(ctx, "dev", "x")
	a, _ := k.CreateServer(ctx, natsSpec("claude-alpha", model.NATSServer))
	_, _ = k.CreateServer(ctx, natsSpec("claude-beta", model.NATSClient))
	if err := k.UpdateNATSMetadata(ctx, a.ID, model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSReady}); err != nil {
		t.Fatal(err)
	}
	meta, err := k.NATSMetadataOf(ctx, a.ID)
	if err != nil || meta == nil || meta.State != model.NATSReady || meta.Role != model.NATSServer {
		t.Errorf("%+v %v", meta, err)
	}
	sts, _ := c.cs.AppsV1().StatefulSets("w").Get(ctx, "claude-alpha", metav1.GetOptions{})
	if sts.Labels["viper-managed"] != "true" || sts.Spec.Template.Labels["viper-nats-state"] != "" {
		t.Errorf("other labels stay, the template is not touched: %v %v", sts.Labels, sts.Spec.Template.Labels)
	}
	members, err := k.ListGroupMachines(ctx, "dev")
	if err != nil || len(members) != 2 {
		t.Fatalf("%+v %v", members, err)
	}
	for _, m := range members {
		if m.Server.PrivateIP != "10.42.0.7" || (m.Server.Name == "claude-alpha") != (m.NATS.State == model.NATSReady) {
			t.Errorf("%+v", m)
		}
	}
	none, _ := k.NATSMetadataOf(ctx, "claude-nothing")
	_ = none
	// labels that make no sense are an error, never a half member
	bad, _ := c.cs.AppsV1().StatefulSets("w").Get(ctx, "claude-beta", metav1.GetOptions{})
	bad.Labels["viper-nats-role"] = "boss"
	_, _ = c.cs.AppsV1().StatefulSets("w").Update(ctx, bad, metav1.UpdateOptions{})
	if _, err := k.ListGroupMachines(ctx, "dev"); err == nil {
		t.Error("a role that is none")
	}
}

func TestAMachineMadeWithoutNATSJoinsByTheGroupLabelOnItsPodAndNetworkDeletionWaitsForTheMachines(t *testing.T) {
	c := newCluster()
	// the pod comes again with the labels of the new template, as the StatefulSet does it
	c.cs.PrependReactor("patch", "statefulsets", func(a k8stesting.Action) (bool, runtime.Object, error) {
		var p struct {
			Spec struct {
				Template struct {
					Metadata struct {
						Labels map[string]string `json:"labels"`
					} `json:"metadata"`
				} `json:"template"`
			} `json:"spec"`
		}
		pa := a.(k8stesting.PatchAction)
		if json.Unmarshal(pa.GetPatch(), &p) == nil && len(p.Spec.Template.Metadata.Labels) > 0 {
			pod, err := c.cs.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), pa.GetNamespace(), pa.GetName()+"-0")
			if err == nil {
				pd := pod.(*corev1.Pod)
				pd.Labels = p.Spec.Template.Metadata.Labels
				_ = c.cs.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), pd, pa.GetNamespace())
			}
		}
		return false, nil, nil
	})
	k := client(c, kubernetes.Options{Namespace: "w", Context: "x"})
	ctx := context.Background()
	plainSpec := spec("claude-plain")
	plainSpec.Labels = nil
	plain, err := k.CreateServer(ctx, plainSpec)
	if err != nil {
		t.Fatal(err)
	}
	n, _, _ := k.EnsurePrivateNetwork(ctx, "dev", "x")
	ip, err := k.AttachPrivateNetwork(ctx, plain.ID, n.ID)
	if err != nil || ip != "10.42.0.7" {
		t.Fatalf("%q %v", ip, err)
	}
	sts, _ := c.cs.AppsV1().StatefulSets("w").Get(ctx, "claude-plain", metav1.GetOptions{})
	if sts.Spec.Template.Labels["viper-nats-group"] != "dev" {
		t.Errorf("%v", sts.Spec.Template.Labels)
	}
	if again, err := k.AttachPrivateNetwork(ctx, plain.ID, n.ID); err != nil || again != ip {
		t.Errorf("attached already: %q %v", again, err)
	}
	_ = k.UpdateNATSMetadata(ctx, plain.ID, model.NATSMetadata{Group: "dev", Role: model.NATSClient, State: model.NATSReady})
	if err := k.DeletePrivateNetwork(ctx, n.ID); !errors.Is(err, cloud.ErrNetworkInUse) {
		t.Errorf("a machine is still in it: %v", err)
	}
	if err := k.DeleteServer(ctx, plain.ID); err != nil {
		t.Fatal(err)
	}
	if err := k.DeletePrivateNetwork(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.cs.NetworkingV1().NetworkPolicies("w").Get(ctx, n.ID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("policy: %v", err)
	}
	if _, err := c.cs.CoreV1().Services("w").Get(ctx, n.ID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("service: %v", err)
	}
	if err := k.DeletePrivateNetwork(ctx, n.ID); err != nil {
		t.Errorf("gone is no error: %v", err)
	}
	// one that is not ours stays
	_, _ = c.cs.NetworkingV1().NetworkPolicies("w").Create(ctx, &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "viper-nats-x", Namespace: "w"}}, metav1.CreateOptions{})
	if err := k.DeletePrivateNetwork(ctx, "viper-nats-x"); err == nil {
		t.Error("not ours")
	}
}
