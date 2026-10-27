package azure

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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
	out := &VM{Name: name}
	if r.Properties != nil && r.Properties.TimeCreated != nil {
		out.Created = *r.Properties.TimeCreated
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

func (s *sdk) CreateVM(ctx context.Context, l Launch) error {
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
					Subnet:          &armnetwork.Subnet{ID: to.Ptr(l.SubnetID)},
					PublicIPAddress: &armnetwork.PublicIPAddress{ID: pip.ID},
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
		Location: to.Ptr(loc), Tags: tags(l.Name),
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
	if _, err := s.groups.CreateOrUpdate(ctx, s.o.group(), armresources.ResourceGroup{Location: to.Ptr(s.o.Location), Tags: map[string]*string{"viper": to.Ptr("managed")}}, nil); err != nil {
		return "", err
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
	prefixes := make([]*string, len(sources))
	for i, s := range sources {
		prefixes[i] = to.Ptr(strings.TrimSpace(s))
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

func (s *sdk) AddNSGRules(ctx context.Context, name string, ssh, icmp bool, sources []string) error {
	add := func(r *armnetwork.SecurityRule) error {
		poller, err := s.rules.BeginCreateOrUpdate(ctx, s.o.group(), name, str(r.Name), *r, nil)
		if err != nil {
			return err
		}
		_, err = poller.PollUntilDone(ctx, nil)
		return err
	}
	if ssh {
		if err := add(rule(sshRule, 100, armnetwork.SecurityRuleProtocolTCP, "22", sources)); err != nil {
			return err
		}
	}
	if icmp {
		return add(rule(icmpRule, 110, armnetwork.SecurityRuleProtocolIcmp, "*", sources))
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
