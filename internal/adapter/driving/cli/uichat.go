package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/chatweb"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
	"github.com/pt9912/hetzner-viper/internal/filelock"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsmcp"
)

// nativeUIChat is viper ui-chat: the chat of docs/plan/ui-chat.md in the browser. Like viper ui it is a web server on 127.0.0.1
// with a key that works once; unlike it, it has no runs and no edits.
func nativeUIChat(o *options) error {
	env, err := o.env()
	if err != nil {
		return err
	}
	dir := filepath.Join(model.StateDir(env.Home), "chat")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	unlock, err := filelock.TryLock(filepath.Join(dir, ".lock"))
	if errors.Is(err, filelock.ErrBusy) {
		return fmt.Errorf("viper ui-chat: it runs already%s (one at a time: both would write the same history)", runningPort(dir))
	}
	if err != nil {
		return err
	}
	defer unlock()
	secrets := &secretValues{}
	b := &uiBackend{o: o, env: env, secrets: secrets}
	b.learnEnvironment()
	cfg := web.Config{Version: o.version, Readonly: o.readonly, Clean: natsmcp.Clean, Redact: secrets.redact, LongestSecret: secrets.longest,
		Chat: chatweb.New(&chatBackend{b: b}, chatweb.Options{Readonly: o.readonly, NoPairs: o.noPairs, Version: o.version})}
	return serveBrowser(o, cfg, "viper ui-chat", func(port string) {
		_ = os.WriteFile(filepath.Join(dir, ".port"), []byte(port+"\n"), 0o600)
	}, func() { _ = os.Remove(filepath.Join(dir, ".port")) })
}

// runningPort says the port of the viper ui-chat that holds the lock, if it wrote it down.
func runningPort(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, ".port"))
	if err != nil {
		return ""
	}
	if p := strings.TrimSpace(string(b)); p != "" {
		if _, err := strconv.Atoi(p); err == nil {
			return " (port " + p + ")"
		}
	}
	return ""
}

// chatBackend tells the chat of the NATS groups of this computer: from the settings, as viper nats reads them.
type chatBackend struct{ b *uiBackend }

func (c *chatBackend) Groups(ctx context.Context) ([]chatweb.Group, error) {
	ov, err := c.b.Overview(ctx)
	if err != nil {
		return nil, err
	}
	var out []chatweb.Group
	for _, ng := range c.b.natsGroups(ov) {
		g, err := c.group(ng)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

// group describes one NATS group: its machines, the topics of their settings, and the name of the operator.
func (c *chatBackend) group(ng web.NATSGroup) (chatweb.Group, error) {
	g := chatweb.Group{Name: ng.Name, Operator: ng.Operator, FileMax: ng.FileMax, OperatorName: model.DefaultNATSOperator}
	loaded, err := loadNatsGroup(c.b.o, c.b.env, ng.Name)
	if err != nil {
		return g, fmt.Errorf("%s: %w", ng.Name, err)
	}
	g.OperatorName = loaded.operator
	topics := map[string]bool{}
	for _, name := range loaded.order {
		m, ok := loaded.ms[name]
		if !ok {
			continue
		}
		cm := chatweb.Machine{Name: name, Topics: m.cfg.NATS.MCPTopics}
		for _, from := range m.cfg.NATS.MCPPushFrom {
			cm.PushFromOperator = cm.PushFromOperator || from == loaded.operator
		}
		for _, t := range m.cfg.NATS.MCPTopics {
			topics[t] = true
		}
		g.Machines = append(g.Machines, cm)
	}
	for t := range topics {
		g.Topics = append(g.Topics, t)
	}
	sort.Strings(g.Topics)
	return g, nil
}
