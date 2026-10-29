package aws_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/aws"
)

type fakeTypes struct{ pages [][]types.InstanceTypeInfo }

func (f *fakeTypes) DescribeInstanceTypes(_ context.Context, in *ec2.DescribeInstanceTypesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	i := 0
	if in.NextToken != nil {
		i, _ = strconv.Atoi(*in.NextToken)
	}
	out := &ec2.DescribeInstanceTypesOutput{InstanceTypes: f.pages[i]}
	if i+1 < len(f.pages) {
		out.NextToken = ptr(fmt.Sprint(i + 1))
	}
	return out, nil
}

type fakePricing struct {
	items   []string
	storage []string // the answer to the question for volumes
	err     error
	asked   *pricing.GetProductsInput
}

func (f *fakePricing) GetProducts(_ context.Context, in *pricing.GetProductsInput, _ ...func(*pricing.Options)) (*pricing.GetProductsOutput, error) {
	for _, flt := range in.Filters {
		if *flt.Field == "productFamily" && *flt.Value == "Storage" {
			return &pricing.GetProductsOutput{PriceList: f.storage}, f.err
		}
	}
	f.asked = in
	return &pricing.GetProductsOutput{PriceList: f.items}, f.err
}

func itype(name string, vcpu int32, mib int64, arch types.ArchitectureType) types.InstanceTypeInfo {
	return types.InstanceTypeInfo{
		InstanceType:  types.InstanceType(name),
		VCpuInfo:      &types.VCpuInfo{DefaultVCpus: ptr(vcpu)},
		MemoryInfo:    &types.MemoryInfo{SizeInMiB: ptr(mib)},
		ProcessorInfo: &types.ProcessorInfo{SupportedArchitectures: []types.ArchitectureType{arch}},
	}
}

func volume(usd string) string {
	return fmt.Sprintf(`{"product":{"attributes":{}},"terms":{"OnDemand":{"A":{"priceDimensions":{"A.B":{"unit":"GB-Mo","pricePerUnit":{"USD":%q}}}}}}}`, usd)
}

func listing(name, usd string) string {
	return fmt.Sprintf(`{"product":{"attributes":{"instanceType":%q}},"terms":{"OnDemand":{"A":{"priceDimensions":{"A.B":{"unit":"Hrs","pricePerUnit":{"USD":%q}}}}}}}`, name, usd)
}

func TestCatalogJoinsTheSizesWithThePrices(t *testing.T) {
	tc := &fakeTypes{pages: [][]types.InstanceTypeInfo{
		{itype("m7i.large", 2, 8192, types.ArchitectureTypeX8664)},
		{itype("m7g.xlarge", 4, 16384, types.ArchitectureTypeArm64), itype("nopriced.large", 2, 4096, types.ArchitectureTypeX8664)},
	}}
	pc := &fakePricing{items: []string{listing("m7i.large", "0.1134"), listing("m7g.xlarge", "0.1632"), "not json", listing("unknown.type", "1")}, storage: []string{volume("0.0952")}}
	offers, err := aws.NewCatalogWith(tc, pc).Offers(context.Background(), "eu-central-1a", 80)
	if err != nil || len(offers) != 2 {
		t.Fatalf("%+v %v", offers, err)
	}
	sort.Slice(offers, func(i, j int) bool { return offers[i].Type < offers[j].Type })
	g := offers[0] // m7g.xlarge
	if g.Provider != "aws" || g.Type != "m7g.xlarge" || g.Location != "eu-central-1a" || g.VCPU != 4 || g.RAMGB != 16 || g.Arch != "arm" || g.DiskGB != 0 || g.SysDiskGB != 80 || abs(g.HourlyPrice-(0.1632+80*0.0952/730)) > 1e-9 || g.Currency != "USD" || g.VATPercent != 0 {
		t.Errorf("%+v", g)
	}
	// 0.1134 * 730 + 80 GB * 0.0952
	if m := offers[1].MonthlyPrice; abs(m-(0.1134*730+80*0.0952)) > 1e-9 {
		t.Errorf("monthly price %v", m)
	}
	// the price list is asked for the region, not the zone
	var region string
	for _, f := range pc.asked.Filters {
		if *f.Field == "regionCode" {
			region = *f.Value
		}
	}
	if region != "eu-central-1" {
		t.Errorf("regionCode %q", region)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestCatalogNeedsALocationAndPassesErrors(t *testing.T) {
	c := aws.NewCatalogWith(&fakeTypes{pages: [][]types.InstanceTypeInfo{{}}}, &fakePricing{err: errors.New("denied")})
	if _, err := c.Offers(context.Background(), "", 0); err == nil {
		t.Error("no location: want an error")
	}
	if _, err := c.Offers(context.Background(), "eu-central-1", 0); err == nil || err.Error() != "denied" {
		t.Errorf("%v", err)
	}
}
