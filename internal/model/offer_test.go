package model_test

import (
	"testing"

	"github.com/pt9912/hetzner-viper/internal/model"
)

func TestOfferFits(t *testing.T) {
	o := model.Offer{VCPU: 4, RAMGB: 8, DiskGB: 80, Arch: "arm", Location: "fsn1"}
	apart := model.Offer{VCPU: 4, RAMGB: 8, Arch: "x86", Location: "eu-central-1"}
	for _, c := range []struct {
		name string
		o    model.Offer
		n    model.Need
		want bool
	}{
		{"exactly", o, model.Need{VCPU: 4, RAMGB: 8, DiskGB: 80}, true},
		{"too few cpus", o, model.Need{VCPU: 5}, false},
		{"too little ram", o, model.Need{RAMGB: 8.5}, false},
		{"too little disk", o, model.Need{DiskGB: 81}, false},
		{"disk chosen apart", apart, model.Need{DiskGB: 500}, true},
		{"arch", o, model.Need{Arch: "x86"}, false},
		{"location", o, model.Need{Location: "nbg1"}, false},
		{"same location", o, model.Need{Location: "fsn1"}, true},
	} {
		if got := c.o.Fits(c.n); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}
