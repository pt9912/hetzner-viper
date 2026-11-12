package azure

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/v2"
)

const (
	vnetName   = "viper-vnet"
	subnetName = "viper-subnet"
	adminUser  = "viper"
	sshRule    = "viper-ssh"
	icmpRule   = "viper-icmp"
)

// sdk is the API over the Azure SDK clients of the subscription and resource group.
type sdk struct {
	o       Options
	groups  *armresources.ResourceGroupsClient
	vms     *armcompute.VirtualMachinesClient
	sizes   *armcompute.VirtualMachineSizesClient
	images  *armcompute.VirtualMachineImagesClient
	pips    *armnetwork.PublicIPAddressesClient
	nics    *armnetwork.InterfacesClient
	asgs    *armnetwork.ApplicationSecurityGroupsClient
	nsgs    *armnetwork.SecurityGroupsClient
	rules   *armnetwork.SecurityRulesClient
	vnets   *armnetwork.VirtualNetworksClient
	subnets *armnetwork.SubnetsClient
}

func newSDK(_ context.Context, o Options) (*sdk, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	s := &sdk{o: o}
	sub := o.SubscriptionID
	if s.groups, err = armresources.NewResourceGroupsClient(sub, cred, nil); err != nil {
		return nil, err
	}
	if s.vms, err = armcompute.NewVirtualMachinesClient(sub, cred, nil); err != nil {
		return nil, err
	}
	if s.sizes, err = armcompute.NewVirtualMachineSizesClient(sub, cred, nil); err != nil {
		return nil, err
	}
	if s.images, err = armcompute.NewVirtualMachineImagesClient(sub, cred, nil); err != nil {
		return nil, err
	}
	if s.pips, err = armnetwork.NewPublicIPAddressesClient(sub, cred, nil); err != nil {
		return nil, err
	}
	if s.nics, err = armnetwork.NewInterfacesClient(sub, cred, nil); err != nil {
		return nil, err
	}
	if s.asgs, err = armnetwork.NewApplicationSecurityGroupsClient(sub, cred, nil); err != nil {
		return nil, err
	}
	if s.nsgs, err = armnetwork.NewSecurityGroupsClient(sub, cred, nil); err != nil {
		return nil, err
	}
	if s.rules, err = armnetwork.NewSecurityRulesClient(sub, cred, nil); err != nil {
		return nil, err
	}
	if s.vnets, err = armnetwork.NewVirtualNetworksClient(sub, cred, nil); err != nil {
		return nil, err
	}
	if s.subnets, err = armnetwork.NewSubnetsClient(sub, cred, nil); err != nil {
		return nil, err
	}
	return s, nil
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func i32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func notFound(err error) bool {
	var re *azcore.ResponseError
	return errors.As(err, &re) && re.StatusCode == http.StatusNotFound
}

func tags(name string) map[string]*string { return map[string]*string{"viper": to.Ptr(name)} }

func (s *sdk) VM(ctx context.Context, name string) (*VM, error) {
	r, err := s.vms.Get(ctx, s.o.group(), name, nil)
	if err != nil {
		if notFound(err) {
			return nil, nil
		}
		return nil, err
	}
	out := &VM{Name: name, Tags: fromTags(r.Tags)}
	if r.Properties != nil && r.Properties.TimeCreated != nil {
		out.Created = *r.Properties.TimeCreated
	}
	if err := s.fillNIC(ctx, out); err != nil {
		return nil, err
	}
	pip, err := s.pips.Get(ctx, s.o.group(), name+"-ip", nil)
	if err != nil && !notFound(err) {
		return nil, err
	}
	if err == nil && pip.Properties != nil && pip.Properties.IPAddress != nil {
		out.IP = *pip.Properties.IPAddress
	}
	return out, nil
}

// CreateVM makes the public address, the network interface and the VM. If that fails, what was
// made before is removed again (best effort): DeleteVM is keyed on the VM, so nothing else would.
func (s *sdk) CreateVM(ctx context.Context, l Launch) error {
	err := s.createVM(ctx, l)
	if err != nil {
		// the run may have been cancelled: the cleanup must not be, so it only keeps the values of ctx
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		s.deleteNetworkParts(cctx, l.Name)
	}
	return err
}

// deleteNetworkParts deletes the network interface and the public address of a VM, ignoring errors
// (a VM that exists still uses them).
func (s *sdk) deleteNetworkParts(ctx context.Context, name string) {
	if p, err := s.nics.BeginDelete(ctx, s.o.group(), name+"-nic", nil); err == nil {
		_, _ = p.PollUntilDone(ctx, nil)
	}
	if p, err := s.pips.BeginDelete(ctx, s.o.group(), name+"-ip", nil); err == nil {
		_, _ = p.PollUntilDone(ctx, nil)
	}
}

func (s *sdk) createVM(ctx context.Context, l Launch) error {
	rg, loc := s.o.group(), s.o.Location
	pipPoller, err := s.pips.BeginCreateOrUpdate(ctx, rg, l.Name+"-ip", armnetwork.PublicIPAddress{
		Location: to.Ptr(loc), Tags: tags(l.Name),
		SKU: &armnetwork.PublicIPAddressSKU{Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard)},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
			PublicIPAddressVersion:   to.Ptr(armnetwork.IPVersionIPv4),
		},
	}, nil)
	if err != nil {
		return err
	}
	pip, err := pipPoller.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}
	nsg, err := s.nsgs.Get(ctx, rg, l.NSG, nil)
	if err != nil {
		return err
	}
	nicPoller, err := s.nics.BeginCreateOrUpdate(ctx, rg, l.Name+"-nic", armnetwork.Interface{
		Location: to.Ptr(loc), Tags: tags(l.Name),
		Properties: &armnetwork.InterfacePropertiesFormat{
			NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: nsg.ID},
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					Subnet:                    &armnetwork.Subnet{ID: to.Ptr(l.SubnetID)},
					PublicIPAddress:           &armnetwork.PublicIPAddress{ID: pip.ID},
					ApplicationSecurityGroups: groupRefs(l.Group),
				},
			}},
		},
	}, nil)
	if err != nil {
		return err
	}
	nic, err := nicPoller.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}
	vmPoller, err := s.vms.BeginCreateOrUpdate(ctx, rg, l.Name, armcompute.VirtualMachine{
		Location: to.Ptr(loc), Tags: launchTags(l),
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{VMSize: to.Ptr(armcompute.VirtualMachineSizeTypes(l.Size))},
			StorageProfile: &armcompute.StorageProfile{
				ImageReference: &armcompute.ImageReference{
					Publisher: to.Ptr(l.Image.Publisher), Offer: to.Ptr(l.Image.Offer), SKU: to.Ptr(l.Image.SKU), Version: to.Ptr(l.Image.Version),
				},
				OSDisk: &armcompute.OSDisk{
					CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesFromImage),
					DeleteOption: to.Ptr(armcompute.DiskDeleteOptionTypesDelete),
					DiskSizeGB:   to.Ptr(l.DiskGB),
					ManagedDisk:  &armcompute.ManagedDiskParameters{StorageAccountType: to.Ptr(armcompute.StorageAccountTypesStandardSSDLRS)},
				},
			},
			OSProfile: &armcompute.OSProfile{
				ComputerName:  to.Ptr(l.Name),
				AdminUsername: to.Ptr(adminUser),
				CustomData:    to.Ptr(base64.StdEncoding.EncodeToString([]byte(l.UserData))),
				LinuxConfiguration: &armcompute.LinuxConfiguration{
					DisablePasswordAuthentication: to.Ptr(true),
					SSH: &armcompute.SSHConfiguration{PublicKeys: []*armcompute.SSHPublicKey{{
						Path: to.Ptr("/home/" + adminUser + "/.ssh/authorized_keys"), KeyData: to.Ptr(strings.TrimSpace(l.SSHKey)),
					}}},
				},
			},
			NetworkProfile: &armcompute.NetworkProfile{NetworkInterfaces: []*armcompute.NetworkInterfaceReference{{
				ID: nic.ID, Properties: &armcompute.NetworkInterfaceReferenceProperties{Primary: to.Ptr(true)},
			}}},
		},
	}, nil)
	if err != nil {
		return err
	}
	_, err = vmPoller.PollUntilDone(ctx, nil)
	return err
}

func (s *sdk) DeleteVM(ctx context.Context, name string) error {
	rg := s.o.group()
	vmPoller, err := s.vms.BeginDelete(ctx, rg, name, nil)
	if err != nil {
		return err
	}
	if _, err := vmPoller.PollUntilDone(ctx, nil); err != nil {
		return err
	}
	nicPoller, err := s.nics.BeginDelete(ctx, rg, name+"-nic", nil)
	if err != nil {
		return err
	}
	if _, err := nicPoller.PollUntilDone(ctx, nil); err != nil {
		return err
	}
	pipPoller, err := s.pips.BeginDelete(ctx, rg, name+"-ip", nil)
	if err != nil {
		return err
	}
	_, err = pipPoller.PollUntilDone(ctx, nil)
	return err
}

func (s *sdk) Subnet(ctx context.Context) (string, error) {
	if s.o.SubnetID != "" {
		return s.o.SubnetID, nil
	}
	r, err := s.subnets.Get(ctx, s.o.group(), vnetName, subnetName, nil)
	if err != nil {
		if notFound(err) {
			return "", nil
		}
		return "", err
	}
	return str(r.ID), nil
}

func (s *sdk) EnsureNetwork(ctx context.Context) (string, error) {
	// an existing group is left as it is: it may be in another location, and a CreateOrUpdate
	// would fail on that (or replace its tags)
	if _, err := s.groups.Get(ctx, s.o.group(), nil); err != nil {
		if !notFound(err) {
			return "", err
		}
		if _, err := s.groups.CreateOrUpdate(ctx, s.o.group(), armresources.ResourceGroup{Location: to.Ptr(s.o.Location), Tags: map[string]*string{"viper": to.Ptr("managed")}}, nil); err != nil {
			return "", err
		}
	}
	if s.o.SubnetID != "" {
		return s.o.SubnetID, nil
	}
	if id, err := s.Subnet(ctx); err != nil || id != "" {
		return id, err
	}
	poller, err := s.vnets.BeginCreateOrUpdate(ctx, s.o.group(), vnetName, armnetwork.VirtualNetwork{
		Location: to.Ptr(s.o.Location), Tags: map[string]*string{"viper": to.Ptr("managed")},
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.77.0.0/16")}},
			Subnets: []*armnetwork.Subnet{{
				Name: to.Ptr(subnetName), Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.77.0.0/24")},
			}},
		},
	}, nil)
	if err != nil {
		return "", err
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return "", err
	}
	return s.Subnet(ctx)
}

func (s *sdk) NSG(ctx context.Context, name string) (*NSG, error) {
	r, err := s.nsgs.Get(ctx, s.o.group(), name, nil)
	if err != nil {
		if notFound(err) {
			return nil, nil
		}
		return nil, err
	}
	out := &NSG{}
	if r.Properties != nil {
		for _, rule := range r.Properties.SecurityRules {
			p := rule.Properties
			if p == nil || p.Access == nil || *p.Access != armnetwork.SecurityRuleAccessAllow || p.Direction == nil || *p.Direction != armnetwork.SecurityRuleDirectionInbound || p.Protocol == nil {
				continue
			}
			switch *p.Protocol {
			case armnetwork.SecurityRuleProtocolTCP:
				out.SSH = out.SSH || str(p.DestinationPortRange) == "22"
			case armnetwork.SecurityRuleProtocolIcmp:
				out.ICMP = true
			}
		}
	}
	return out, nil
}

func rule(name string, priority int32, proto armnetwork.SecurityRuleProtocol, port string, sources []string) *armnetwork.SecurityRule {
	var prefixes []*string
	for _, s := range sources {
		if s = strings.TrimSpace(s); s != "" {
			prefixes = append(prefixes, to.Ptr(s))
		}
	}
	return &armnetwork.SecurityRule{
		Name: to.Ptr(name),
		Properties: &armnetwork.SecurityRulePropertiesFormat{
			Priority: to.Ptr(priority), Direction: to.Ptr(armnetwork.SecurityRuleDirectionInbound), Access: to.Ptr(armnetwork.SecurityRuleAccessAllow),
			Protocol: to.Ptr(proto), SourcePortRange: to.Ptr("*"), DestinationPortRange: to.Ptr(port),
			SourceAddressPrefixes: prefixes, DestinationAddressPrefix: to.Ptr("*"),
		},
	}
}

func (s *sdk) CreateNSG(ctx context.Context, name string, sources []string) error {
	poller, err := s.nsgs.BeginCreateOrUpdate(ctx, s.o.group(), name, armnetwork.SecurityGroup{
		Location: to.Ptr(s.o.Location), Tags: tags(name),
		Properties: &armnetwork.SecurityGroupPropertiesFormat{SecurityRules: []*armnetwork.SecurityRule{
			rule(sshRule, 100, armnetwork.SecurityRuleProtocolTCP, "22", sources),
			rule(icmpRule, 110, armnetwork.SecurityRuleProtocolIcmp, "*", sources),
		}},
	}, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// freePriorities returns n priorities (100, 110, ...) no rule of the group has.
func (s *sdk) freePriorities(ctx context.Context, name string, n int) ([]int32, error) {
	r, err := s.nsgs.Get(ctx, s.o.group(), name, nil)
	if err != nil {
		return nil, err
	}
	used := map[int32]bool{}
	if r.Properties != nil {
		for _, rule := range r.Properties.SecurityRules {
			if rule.Properties != nil && rule.Properties.Priority != nil {
				used[*rule.Properties.Priority] = true
			}
		}
	}
	var out []int32
	for p := int32(100); len(out) < n && p <= 4096; p += 10 {
		if !used[p] {
			out = append(out, p)
		}
	}
	if len(out) < n {
		return nil, errors.New("the network security group has no free rule priority")
	}
	return out, nil
}

func (s *sdk) AddNSGRules(ctx context.Context, name string, ssh, icmp bool, sources []string) error {
	n := 0
	for _, b := range []bool{ssh, icmp} {
		if b {
			n++
		}
	}
	prios, err := s.freePriorities(ctx, name, n)
	if err != nil {
		return err
	}
	add := func(r *armnetwork.SecurityRule) error {
		poller, err := s.rules.BeginCreateOrUpdate(ctx, s.o.group(), name, str(r.Name), *r, nil)
		if err != nil {
			return err
		}
		_, err = poller.PollUntilDone(ctx, nil)
		return err
	}
	if ssh {
		if err := add(rule(sshRule, prios[0], armnetwork.SecurityRuleProtocolTCP, "22", sources)); err != nil {
			return err
		}
		prios = prios[1:]
	}
	if icmp {
		return add(rule(icmpRule, prios[0], armnetwork.SecurityRuleProtocolIcmp, "*", sources))
	}
	return nil
}

func (s *sdk) VMSize(ctx context.Context, name string) (*Size, error) {
	pager := s.sizes.NewListPager(s.o.Location, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, sz := range page.Value {
			if strings.EqualFold(str(sz.Name), name) {
				return &Size{VCPUs: i32(sz.NumberOfCores), MemoryMB: i32(sz.MemoryInMB)}, nil
			}
		}
	}
	return nil, nil
}

func (s *sdk) ImageExists(ctx context.Context, img Image) (bool, error) {
	r, err := s.images.List(ctx, s.o.Location, img.Publisher, img.Offer, img.SKU, &armcompute.VirtualMachineImagesClientListOptions{Top: to.Ptr(int32(1))})
	if err != nil {
		if notFound(err) {
			return false, nil
		}
		return false, err
	}
	return len(r.VirtualMachineImageResourceArray) > 0, nil
}

func fromTags(t map[string]*string) map[string]string {
	out := make(map[string]string, len(t))
	for k, v := range t {
		out[k] = str(v)
	}
	return out
}

// launchTags are the tags of a new VM: viper=<name> and the extra ones (its NATS labels).
func launchTags(l Launch) map[string]*string {
	t := tags(l.Name)
	for k, v := range l.Tags {
		t[k] = to.Ptr(v)
	}
	return t
}

func groupRefs(id string) []*armnetwork.ApplicationSecurityGroup {
	if id == "" {
		return nil
	}
	return []*armnetwork.ApplicationSecurityGroup{{ID: to.Ptr(id)}}
}

func lastSegment(id string) string {
	if i := strings.LastIndex(id, "/"); i >= 0 {
		return id[i+1:]
	}
	return id
}

// fillNIC reads the private address and the application security groups of the interface of the VM.
func (s *sdk) fillNIC(ctx context.Context, v *VM) error {
	nic, err := s.nics.Get(ctx, s.o.group(), v.Name+"-nic", nil)
	if err != nil {
		if notFound(err) {
			return nil
		}
		return err
	}
	if nic.Properties == nil {
		return nil
	}
	for _, c := range nic.Properties.IPConfigurations {
		if c.Properties == nil {
			continue
		}
		if v.PrivateIP == "" {
			v.PrivateIP = str(c.Properties.PrivateIPAddress)
		}
		for _, g := range c.Properties.ApplicationSecurityGroups {
			v.InGroups = append(v.InGroups, lastSegment(str(g.ID)))
		}
	}
	return nil
}

func (s *sdk) CreateGroupASG(ctx context.Context, name string, t map[string]string) error {
	tg := map[string]*string{}
	for k, v := range t {
		tg[k] = to.Ptr(v)
	}
	// the header only goes with the PUT that makes the group: a GET that polls for it would be refused
	put := policy.WithHTTPHeader(ctx, http.Header{"If-None-Match": []string{"*"}})
	poller, err := s.asgs.BeginCreateOrUpdate(put, s.o.group(), name, armnetwork.ApplicationSecurityGroup{Location: to.Ptr(s.o.Location), Tags: tg}, nil)
	var re *azcore.ResponseError
	if errors.As(err, &re) && (re.StatusCode == http.StatusPreconditionFailed || (re.StatusCode == http.StatusConflict && re.ErrorCode != "AnotherOperationInProgress")) {
		return ErrExists
	}
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

func (s *sdk) GroupASG(ctx context.Context, name string) (*GroupASG, error) {
	r, err := s.asgs.Get(ctx, s.o.group(), name, nil)
	if err != nil {
		if notFound(err) {
			return nil, nil
		}
		return nil, err
	}
	out := &GroupASG{ID: str(r.ID), Name: name, Location: str(r.Location), Tags: fromTags(r.Tags)}
	return out, nil
}

func (s *sdk) DeleteGroupASG(ctx context.Context, name string) error {
	poller, err := s.asgs.BeginDelete(ctx, s.o.group(), name, nil)
	var re *azcore.ResponseError
	if errors.As(err, &re) && (re.ErrorCode == "ApplicationSecurityGroupInUse" || re.StatusCode == http.StatusConflict) {
		return ErrInUse
	}
	if err != nil {
		if notFound(err) {
			return nil
		}
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// groupRule is the rule of a NATS group in a network security group.
func groupRule(group, asgID string, port int, priority int32) *armnetwork.SecurityRule {
	return &armnetwork.SecurityRule{
		Name: to.Ptr(natsPrefix + group),
		Properties: &armnetwork.SecurityRulePropertiesFormat{
			Priority: to.Ptr(priority), Direction: to.Ptr(armnetwork.SecurityRuleDirectionInbound), Access: to.Ptr(armnetwork.SecurityRuleAccessAllow),
			Protocol: to.Ptr(armnetwork.SecurityRuleProtocolTCP), SourcePortRange: to.Ptr("*"), DestinationPortRange: to.Ptr(fmt.Sprint(port)),
			SourceApplicationSecurityGroups:      []*armnetwork.ApplicationSecurityGroup{{ID: to.Ptr(asgID)}},
			DestinationApplicationSecurityGroups: []*armnetwork.ApplicationSecurityGroup{{ID: to.Ptr(asgID)}},
		},
	}
}

func (s *sdk) AllowGroup(ctx context.Context, nsg, group, asgID string, port int) error {
	r, err := s.nsgs.Get(ctx, s.o.group(), nsg, nil)
	if err != nil {
		return err
	}
	if r.Properties != nil {
		for _, rule := range r.Properties.SecurityRules {
			if str(rule.Name) == natsPrefix+group {
				return nil // there already
			}
		}
	}
	prios, err := s.freePriorities(ctx, nsg, 1)
	if err != nil {
		return err
	}
	poller, err := s.rules.BeginCreateOrUpdate(ctx, s.o.group(), nsg, natsPrefix+group, *groupRule(group, asgID, port, prios[0]), nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

func (s *sdk) DropGroupRule(ctx context.Context, group string) error {
	pager := s.nsgs.NewListPager(s.o.group(), nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, n := range page.Value {
			if err := s.dropRule(ctx, n, group); err != nil {
				return err
			}
		}
	}
	return nil
}

// dropRule deletes the rule of the group from the network security group, if it has it.
func (s *sdk) dropRule(ctx context.Context, n *armnetwork.SecurityGroup, group string) error {
	if n.Properties == nil {
		return nil
	}
	for _, rule := range n.Properties.SecurityRules {
		if str(rule.Name) != natsPrefix+group {
			continue
		}
		poller, err := s.rules.BeginDelete(ctx, s.o.group(), str(n.Name), natsPrefix+group, nil)
		if err != nil {
			return err
		}
		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *sdk) ListVMs(ctx context.Context, want map[string]string) ([]*VM, error) {
	var out []*VM
	pager := s.vms.NewListPager(s.o.group(), nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
	vms:
		for _, r := range page.Value {
			t := fromTags(r.Tags)
			for k, v := range want {
				if t[k] != v {
					continue vms
				}
			}
			v := &VM{Name: str(r.Name), Tags: t}
			if r.Properties != nil && r.Properties.TimeCreated != nil {
				v.Created = *r.Properties.TimeCreated
			}
			if err := s.fillNIC(ctx, v); err != nil {
				return nil, err
			}
			out = append(out, v)
		}
	}
	return out, nil
}

func (s *sdk) SetVMTags(ctx context.Context, name string, t map[string]string) error {
	have, err := s.vms.Get(ctx, s.o.group(), name, nil)
	if err != nil {
		return err
	}
	merged := have.Tags
	if merged == nil {
		merged = map[string]*string{}
	}
	for k, v := range t {
		merged[k] = to.Ptr(v)
	}
	poller, err := s.vms.BeginUpdate(ctx, s.o.group(), name, armcompute.VirtualMachineUpdate{Tags: merged}, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

func (s *sdk) JoinGroup(ctx context.Context, vm, group, asgID string, port int) error {
	nic, err := s.nics.Get(ctx, s.o.group(), vm+"-nic", nil)
	if err != nil {
		return err
	}
	if nic.Properties == nil || len(nic.Properties.IPConfigurations) == 0 || nic.Properties.IPConfigurations[0].Properties == nil || nic.Properties.NetworkSecurityGroup == nil {
		return errors.New("the network interface has no IP configuration or no network security group")
	}
	if err := s.AllowGroup(ctx, lastSegment(str(nic.Properties.NetworkSecurityGroup.ID)), group, asgID, port); err != nil {
		return err
	}
	cfg := nic.Properties.IPConfigurations[0].Properties
	for _, g := range cfg.ApplicationSecurityGroups {
		if str(g.ID) == asgID {
			return nil // in it already
		}
	}
	cfg.ApplicationSecurityGroups = append(cfg.ApplicationSecurityGroups, &armnetwork.ApplicationSecurityGroup{ID: to.Ptr(asgID)})
	poller, err := s.nics.BeginCreateOrUpdate(ctx, s.o.group(), vm+"-nic", nic.Interface, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}
