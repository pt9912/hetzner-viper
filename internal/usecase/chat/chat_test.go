package chat_test

import (
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/usecase/chat"
)

func group() chat.Group {
	return chat.Group{Operator: "operator", Machines: map[string]bool{"w1": true, "w2": true, "w3": true, "a-b": true, "c": true}}
}

func msg(via, from, to, topic, typ string) model.NATSLine {
	return model.NATSLine{Event: "message", Via: via, From: from, To: to, Topic: topic, Type: typ, Text: "x"}
}

func TestEachLineGoesToTheConversationsItBelongsToAndAlwaysToAll(t *testing.T) {
	cases := []struct {
		name string
		l    model.NATSLine
		want []string
		unk  bool
	}{
		{"a machine writes to the operator", msg("out", "w1", "operator", "", "msg"), []string{"rechner__w1", "alle"}, false},
		{"a question of a machine to the operator", msg("out", "w2", "operator", "", "req"), []string{"rechner__w2", "alle"}, false},
		{"the operator writes to a machine (seen from another computer)", msg("out", "operator", "w3", "", "msg"), []string{"rechner__w3", "alle"}, false},
		{"two machines: the pair, the same in both directions", msg("out", "w2", "w1", "", "msg"), []string{"paar__w1__w2", "alle"}, false},
		{"the other direction", msg("out", "w1", "w2", "", "res"), []string{"paar__w1__w2", "alle"}, false},
		{"a topic", msg("pub", "w1", "", "build", "msg"), []string{"thema__build", "alle"}, false},
		{"a pair with dashes in the names is not the pair of other names", msg("out", "a-b", "c", "", "msg"), []string{"paar__a-b__c", "alle"}, false},
		{"a name that is no machine of the group: only all", msg("out", "w1", "ghost", "", "msg"), []string{"alle"}, true},
		{"a sender that is no machine: only all", msg("pub", "ghost", "", "build", "msg"), []string{"alle"}, true},
		{"a topic with a bad name: only all", msg("pub", "w1", "", "Bad_Name", "msg"), []string{"alle"}, true},
		{"itself", msg("out", "w1", "w1", "", "msg"), []string{"alle"}, true},
	}
	for _, c := range cases {
		r := chat.Classify(group(), c.l)
		if r.Skip || len(r.Keys) != len(c.want) || r.Unknown != c.unk {
			t.Errorf("%s: %+v", c.name, r)
			continue
		}
		for i := range c.want {
			if r.Keys[i] != c.want[i] {
				t.Errorf("%s: %v, not %v", c.name, r.Keys, c.want)
			}
		}
	}
}

func TestPresenceAndTheListOfTopicsAreNoMessagesAndPairsCanBeLeftOut(t *testing.T) {
	for _, l := range []model.NATSLine{msg("presence", "w1", "", "", "presence"), msg("topics", "w1", "", "", "topics"), {Event: "ready"}, {Event: "sent"}} {
		if !chat.Classify(group(), l).Skip {
			t.Errorf("%+v is no message", l)
		}
	}
	g := group()
	g.NoPairs = true
	if r := chat.Classify(g, msg("out", "w1", "w2", "", "msg")); len(r.Keys) != 1 || r.Keys[0] != "alle" {
		t.Errorf("--no-pairs: %+v", r)
	}
}

func TestThePairKeyIsTheSameInBothDirectionsAndTheKeysAreUnambiguous(t *testing.T) {
	if chat.PairKey("w2", "w1") != chat.PairKey("w1", "w2") {
		t.Error("a pair has one key")
	}
	if chat.PairKey("a", "b-c") == chat.PairKey("a-b", "c") {
		t.Error("the pairs (a, b-c) and (a-b, c) are two")
	}
}

func TestPresenceSaysHereGoneOrUnknownAndTheSubscribersOfATopic(t *testing.T) {
	now := time.Date(2026, 11, 18, 14, 0, 0, 0, time.UTC)
	p := chat.NewPresence(90 * time.Second)
	if p.State("w1", now) != "unknown" {
		t.Error("a machine that said nothing is unknown")
	}
	p.Update(model.NATSLine{Event: "message", Via: "presence", From: "w1", State: "up"}, now)
	p.Update(model.NATSLine{Event: "message", Via: "topics", From: "w1", Text: "build general"}, now)
	p.Update(model.NATSLine{Event: "message", Via: "presence", From: "w2", State: "up"}, now)
	p.Update(model.NATSLine{Event: "message", Via: "topics", From: "w2", Text: "general"}, now)
	if p.State("w1", now.Add(60*time.Second)) != "here" || p.State("w1", now.Add(91*time.Second)) != "gone" {
		t.Error("here for 90 seconds")
	}
	p.Update(model.NATSLine{Event: "message", Via: "presence", From: "w2", State: "bye"}, now.Add(10*time.Second))
	if p.State("w2", now.Add(11*time.Second)) != "gone" {
		t.Error("bye is gone at once")
	}
	if got := p.Subscribers("build"); len(got) != 1 || got[0] != "w1" {
		t.Errorf("subscribers of build: %v", got)
	}
	if len(p.Subscribers("general")) != 2 || len(p.Subscribers("nobody")) != 0 || !p.Reported("w1") || p.Reported("w3") {
		t.Error("subscribers and who reported")
	}
}

func TestTheLimiterTakesSomeLinesPerSecondInAllAndPerSenderAndCountsWhatItDrops(t *testing.T) {
	var l chat.Limiter
	now := time.Date(2026, 11, 18, 14, 0, 0, 0, time.UTC)
	taken := 0
	for i := 0; i < 100; i++ {
		if l.Allow("w1", now) {
			taken++
		}
	}
	if taken != chat.MaxLinesPerSenderSecond || l.Dropped != 100-chat.MaxLinesPerSenderSecond {
		t.Errorf("one sender: %d taken, %d dropped", taken, l.Dropped)
	}
	taken = 0
	for i := 0; i < 1000; i++ {
		if l.Allow([]string{"a", "b", "c", "d", "e", "f"}[i%6], now) {
			taken++
		}
	}
	if taken+chat.MaxLinesPerSenderSecond > chat.MaxLinesPerSecond+chat.MaxLinesPerSenderSecond || taken < 100 {
		t.Errorf("all senders: %d", taken)
	}
	if !l.Allow("w1", now.Add(time.Second)) {
		t.Error("the next second starts anew")
	}
}

func TestANewTopicIsAdmittedUnlessTheSenderMadeTooManyThisHourOrThereAreTooManyConversations(t *testing.T) {
	var a chat.Admission
	now := time.Date(2026, 11, 18, 14, 0, 0, 0, time.UTC)
	ok := 0
	for i := 0; i < 150; i++ {
		if a.Topic("w1", 10, now) {
			ok++
		}
	}
	if ok != chat.MaxNewTopicsPerHour || a.Denied != 50 {
		t.Errorf("%d admitted, %d denied", ok, a.Denied)
	}
	if !a.Topic("w1", 10, now.Add(61*time.Minute)) {
		t.Error("an hour later it may again")
	}
	if a.Topic("w2", chat.MaxConversations, now) {
		t.Error("the limit of conversations")
	}
}
