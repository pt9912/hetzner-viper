package model_test

import (
	"testing"

	"github.com/pt9912/hetzner-viper/internal/model"
)

func TestCheckNATSName(t *testing.T) {
	long := "a234567890123456789012345678901" // 31
	for name, ok := range map[string]bool{
		"entwicklung": true, "a": true, "a-b": true, "x1": true, long: true, long + "b": false,
		"": false, "-a": false, "a-": false, "A": false, "a.b": false, "a*": false, "a>": false,
		"a b": false, "a_b": false, "ä": false,
	} {
		if err := model.CheckNATSName("NATS_GROUP", name); (err == nil) != ok {
			t.Errorf("%q: ok=%v, err=%v", name, ok, err)
		}
	}
}

func TestParseNATSBool(t *testing.T) {
	for in, want := range map[string]bool{"true": true, "TRUE": true, "1": true, " true ": true, "": false, "0": false, "false": false} {
		if got, err := model.ParseNATSBool("NATS_ENABLED", in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"yes", "on", "2"} {
		if _, err := model.ParseNATSBool("NATS_ENABLED", in); err == nil {
			t.Errorf("%q must be refused", in)
		}
	}
}

func TestParseNATSPort(t *testing.T) {
	if p, err := model.ParseNATSPort(""); err != nil || p != model.DefaultNATSPort {
		t.Errorf("default: %d %v", p, err)
	}
	if p, err := model.ParseNATSPort("4333"); err != nil || p != 4333 {
		t.Errorf("4333: %d %v", p, err)
	}
	for _, in := range []string{"22", "70000", "x", "-1", "1023"} {
		if _, err := model.ParseNATSPort(in); err == nil {
			t.Errorf("%q must be refused", in)
		}
	}
}

func TestNATSLabelsRoundTrip(t *testing.T) {
	in := model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSReady}
	out, ok, err := model.NATSMetadataFromLabels(model.NATSLabels(in))
	if err != nil || !ok || out != in {
		t.Fatalf("%+v %v %v", out, ok, err)
	}
	if _, ok, err := model.NATSMetadataFromLabels(map[string]string{"team": "x"}); ok || err != nil {
		t.Errorf("a server without NATS labels is no member and no error: %v %v", ok, err)
	}
}

func TestNATSMetadataFromLabelsRefusesNonsense(t *testing.T) {
	good := model.NATSLabels(model.NATSMetadata{Group: "dev", Role: model.NATSClient, State: model.NATSFailed})
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range good {
			m[a] = b
		}
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}
	for name, l := range map[string]map[string]string{
		"role":     with(model.LabelNATSRole, "boss"),
		"state":    with(model.LabelNATSState, "done"),
		"group":    with(model.LabelNATSGroup, "A.b"),
		"no role":  with(model.LabelNATSRole, ""),
		"no state": with(model.LabelNATSState, ""),
		"no group": with(model.LabelNATSGroup, ""),
	} {
		if _, ok, err := model.NATSMetadataFromLabels(l); ok || err == nil {
			t.Errorf("%s: ok=%v err=%v", name, ok, err)
		}
	}
}
