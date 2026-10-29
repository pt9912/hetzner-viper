package model

// Offer is one server type of a provider in one location, as the provider's price list gives it.
type Offer struct {
	Provider string // "hetzner", ...
	Type     string // the value of SERVER_TYPE
	Location string // the value of SERVER_LOCATION
	VCPU     int
	RAMGB    float64
	// DiskGB is the disk that comes with the type; 0 if the disk is chosen apart from the type
	// (the *_ROOT_GB settings).
	DiskGB       int
	Arch         string  // "x86" or "arm"
	HourlyPrice  float64 // list price in Currency, with VAT if VATPercent is not 0
	MonthlyPrice float64
	Currency     string
	// SysDiskGB is the size of the system disk that is chosen apart from the type and whose price
	// is in the prices; 0 if the disk comes with the type (DiskGB) or is not priced.
	SysDiskGB int
	// VATPercent is the VAT in the prices (19 for 19 %); 0 if they are without VAT.
	VATPercent float64
}

// Need is what a machine at least has to offer.
type Need struct {
	VCPU   int
	RAMGB  float64
	DiskGB int
	Arch   string // "" for any
	// Location limits the offers to one location; "" for any.
	Location string
	// Provider limits the search to one provider; "" for all.
	Provider string
}

// Fits reports whether the offer has what the need asks for.
func (o Offer) Fits(n Need) bool {
	if o.VCPU < n.VCPU || o.RAMGB < n.RAMGB {
		return false
	}
	// a disk chosen apart from the type (DiskGB 0) can be made as large as needed
	if o.DiskGB != 0 && o.DiskGB < n.DiskGB {
		return false
	}
	if n.Arch != "" && o.Arch != n.Arch {
		return false
	}
	return n.Location == "" || o.Location == n.Location
}
