package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
	"github.com/pt9912/hetzner-viper/internal/model"
	"golang.org/x/sync/errgroup"
)

// Pricing is the part of the Price List API the catalog uses. The API is only served from a few
// regions (us-east-1 is one), whatever the region of the prices.
type Pricing interface {
	GetProducts(ctx context.Context, in *pricing.GetProductsInput, opts ...func(*pricing.Options)) (*pricing.GetProductsOutput, error)
}

// Types is the part of the EC2 API that describes the instance types of a region.
type Types interface {
	DescribeInstanceTypes(ctx context.Context, in *ec2.DescribeInstanceTypesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error)
}

// Catalog is the price list of the on-demand Linux instances of EC2: the types and their size
// from EC2, their prices from the Price List API. It needs the same credentials as the rest.
type Catalog struct {
	profile string
	// make the clients for a region; replaced in the tests
	clients func(ctx context.Context, region string) (clients, error)
}

// clients are the APIs of the catalog.
type clients struct {
	types   Types
	pricing Pricing
}

// NewCatalog returns a catalog; profile is AWS_PROFILE, "" for the default credentials chain.
func NewCatalog(profile string) *Catalog {
	c := &Catalog{profile: profile}
	c.clients = c.sdkClients
	return c
}

// NewCatalogWith returns a catalog over given APIs (for tests).
func NewCatalogWith(t Types, p Pricing) *Catalog {
	return &Catalog{clients: func(context.Context, string) (clients, error) { return clients{t, p}, nil }}
}

func (c *Catalog) sdkClients(ctx context.Context, region string) (clients, error) {
	loaders := []func(*config.LoadOptions) error{config.WithRegion(region)}
	if c.profile != "" {
		loaders = append(loaders, config.WithSharedConfigProfile(c.profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, loaders...)
	if err != nil {
		return clients{}, err
	}
	pcfg := cfg
	pcfg.Region = "us-east-1"
	return clients{ec2.NewFromConfig(cfg), pricing.NewFromConfig(pcfg)}, nil
}

// Describe is an API error without the request details, safe to show.
func (*Catalog) Describe(err error) string { return (*Client)(nil).Describe(err) }

// Offers lists the instance types of the region of the location that have a list price, with the
// price of a gp3 system disk of diskGB (50 GB for 0) added. The prices are in US dollars without VAT.
func (c *Catalog) Offers(ctx context.Context, location string, diskGB int) ([]model.Offer, error) {
	region := Options{Location: location}.region()
	if region == "" {
		return nil, fmt.Errorf("no region: give --location or SERVER_LOCATION")
	}
	api, err := c.clients(ctx, region)
	if err != nil {
		return nil, err
	}
	if diskGB <= 0 {
		diskGB = int(Options{}.rootGB())
	}
	// the three reads do not depend on each other, and the price list of the instances takes
	// many round trips
	var (
		sizes  map[string]size
		prices map[string]float64
		perGB  float64
		g, gc  = errgroup.WithContext(ctx)
	)
	g.Go(func() (err error) { sizes, err = instanceTypes(gc, api.types); return err })
	g.Go(func() (err error) { prices, err = hourlyPrices(gc, api.pricing, region); return err })
	g.Go(func() (err error) { perGB, err = gp3Price(gc, api.pricing, region); return err })
	if err := g.Wait(); err != nil {
		return nil, err
	}
	disk := float64(diskGB) * perGB // per month
	var out []model.Offer
	for name, s := range sizes {
		if p, ok := prices[name]; ok {
			out = append(out, model.Offer{
				Provider: "aws", Type: name, Location: location, VCPU: s.vcpu, RAMGB: s.ramGB, Arch: s.arch, SysDiskGB: diskGB,
				HourlyPrice: p + disk/model.HoursPerMonth, MonthlyPrice: p*model.HoursPerMonth + disk, Currency: "USD",
			})
		}
	}
	return out, nil
}

type size struct {
	vcpu  int
	ramGB float64
	arch  string
}

// instanceTypes are the types that can run on demand, by name.
func instanceTypes(ctx context.Context, api Types) (map[string]size, error) {
	out := map[string]size{}
	in := &ec2.DescribeInstanceTypesInput{Filters: []types.Filter{{Name: awssdk.String("supported-usage-class"), Values: []string{"on-demand"}}}}
	for {
		page, err := api.DescribeInstanceTypes(ctx, in)
		if err != nil {
			return nil, err
		}
		for _, t := range page.InstanceTypes {
			if s, ok := sizeOf(t); ok {
				out[string(t.InstanceType)] = s
			}
		}
		if page.NextToken == nil {
			return out, nil
		}
		in.NextToken = page.NextToken
	}
}

// sizeOf is the cores, the memory and the architecture of a type; ok is false for a type that has
// no x86 or Arm processor.
func sizeOf(t types.InstanceTypeInfo) (s size, ok bool) {
	if t.VCpuInfo == nil || t.MemoryInfo == nil || t.ProcessorInfo == nil {
		return size{}, false
	}
	s = size{vcpu: int(awssdk.ToInt32(t.VCpuInfo.DefaultVCpus)), ramGB: float64(awssdk.ToInt64(t.MemoryInfo.SizeInMiB)) / 1024}
	for _, a := range t.ProcessorInfo.SupportedArchitectures {
		switch a {
		case types.ArchitectureTypeX8664:
			s.arch = "x86"
		case types.ArchitectureTypeArm64:
			s.arch = "arm"
		}
	}
	return s, s.arch != ""
}

// product is the part of a price list entry the catalog reads.
type product struct {
	Product struct {
		Attributes struct {
			InstanceType string `json:"instanceType"`
		} `json:"attributes"`
	} `json:"product"`
	Terms struct {
		OnDemand map[string]struct {
			PriceDimensions map[string]struct {
				Unit         string            `json:"unit"`
				PricePerUnit map[string]string `json:"pricePerUnit"`
			} `json:"priceDimensions"`
		} `json:"OnDemand"`
	} `json:"terms"`
}

// hourlyPrices are the on-demand prices in US dollars of Linux instances on shared hardware.
func hourlyPrices(ctx context.Context, api Pricing, region string) (map[string]float64, error) {
	match := func(field, value string) pricingtypes.Filter {
		return pricingtypes.Filter{Type: pricingtypes.FilterTypeTermMatch, Field: awssdk.String(field), Value: awssdk.String(value)}
	}
	in := &pricing.GetProductsInput{
		ServiceCode: awssdk.String("AmazonEC2"),
		Filters: []pricingtypes.Filter{
			match("regionCode", region), match("operatingSystem", "Linux"), match("tenancy", "Shared"),
			match("preInstalledSw", "NA"), match("capacitystatus", "Used"), match("productFamily", "Compute Instance"),
		},
	}
	out := map[string]float64{}
	for {
		page, err := api.GetProducts(ctx, in)
		if err != nil {
			return nil, err
		}
		for _, item := range page.PriceList {
			var p product
			if json.Unmarshal([]byte(item), &p) != nil {
				continue
			}
			if usd, ok := hourlyUSD(p); ok && p.Product.Attributes.InstanceType != "" {
				out[p.Product.Attributes.InstanceType] = usd
			}
		}
		if page.NextToken == nil {
			return out, nil
		}
		in.NextToken = page.NextToken
	}
}

// gp3Price is the price of a GB of gp3 volume for a month in US dollars.
func gp3Price(ctx context.Context, api Pricing, region string) (float64, error) {
	in := &pricing.GetProductsInput{
		ServiceCode: awssdk.String("AmazonEC2"),
		Filters: []pricingtypes.Filter{
			{Type: pricingtypes.FilterTypeTermMatch, Field: awssdk.String("regionCode"), Value: awssdk.String(region)},
			{Type: pricingtypes.FilterTypeTermMatch, Field: awssdk.String("productFamily"), Value: awssdk.String("Storage")},
			{Type: pricingtypes.FilterTypeTermMatch, Field: awssdk.String("volumeApiName"), Value: awssdk.String("gp3")},
		},
	}
	for {
		page, err := api.GetProducts(ctx, in)
		if err != nil {
			return 0, err
		}
		for _, item := range page.PriceList {
			var p product
			if json.Unmarshal([]byte(item), &p) != nil {
				continue
			}
			if v, ok := priceOf(p, "GB-Mo"); ok {
				return v, nil
			}
		}
		if page.NextToken == nil {
			return 0, fmt.Errorf("no price of gp3 volumes in %s", region)
		}
		in.NextToken = page.NextToken
	}
}

func hourlyUSD(p product) (float64, bool) { return priceOf(p, "Hrs") }

// priceOf is the on-demand price in US dollars per unit ("Hrs", "GB-Mo").
func priceOf(p product, unit string) (float64, bool) {
	for _, term := range p.Terms.OnDemand {
		for _, d := range term.PriceDimensions {
			if d.Unit != unit {
				continue
			}
			if v, err := strconv.ParseFloat(d.PricePerUnit["USD"], 64); err == nil && v > 0 {
				return v, true
			}
		}
	}
	return 0, false
}
