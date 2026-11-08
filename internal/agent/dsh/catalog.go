package dsh

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kumoSleeping/iridium/internal/agent"
)

// The models dsh offers (the user, 2026-11-08: not a list of Iridium's own): what its sdk profile
// registers where a session runs, every provider's (dsh-llm's listProviders, listModels and each
// model's efforts), and the default (dsh-agent-default-model). Iridium asks dsh itself: a probe, a
// cordis plugin the --patch it boots with inserts, prints them on one line and exits.

// probeMark starts the probe's line of output.
const probeMark = "IRIDIUM-CATALOG "

// ProbeModule is the probe plugin: once the providers have registered, it prints the catalog and
// ends the process.
const ProbeModule = `// Iridium's probe: the models this dsh offers, printed on one line; then it exits.
export const name = 'iridium-catalog-probe'
export const inject = ['llm']
export function apply(ctx) {
  const run = async () => {
    // The adapters register their providers as they start, after the llm service.
    await new Promise((r) => setTimeout(r, 1500))
    const providers = []
    for (const p of ctx.llm.listProviders()) {
      const models = []
      for (const m of await ctx.llm.listModels(p.id).catch(() => [])) {
        const r = await ctx.llm.resolveModelInfo(p.id, m.id).catch(() => undefined)
        models.push({
          id: m.id, name: m.name, description: m.description,
          efforts: r?.reasoning?.efforts?.map((e) => e.id), defaultEffort: r?.reasoning?.defaultEffort,
        })
      }
      providers.push({ id: p.id, name: p.name, models })
    }
    const sel = ctx.get('agentDefaultModel')?.currentSelection?.()
    process.stdout.write('` + probeMark + `' + JSON.stringify({ providers, default: sel ?? null }) + '\n')
  }
  run().catch((e) => process.stderr.write('iridium probe: ' + (e?.stack ?? e) + '\n')).finally(() => process.exit(0))
}
`

// ProbePatch is the --patch that loads the probe from moduleURL (a file: URL there).
func ProbePatch(moduleURL string) []byte {
	return []byte("# Iridium: a probe of the models this dsh offers; it exits once it has printed them.\n- insert:\n    - id: iridium-catalog-probe\n      name: '" + strings.ReplaceAll(moduleURL, "'", "''") + "'\n")
}

// FileURL is path's file: URL ('/'-separated; a Windows path's drive after a third '/').
func FileURL(path string) string {
	p := strings.ReplaceAll(path, `\`, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + strings.ReplaceAll(strings.ReplaceAll(p, "%", "%25"), " ", "%20")
}

// ProbeArgs are what follow dsh's program to run the probe of patch, after the patches more (a
// session's own, Remote.Patches).
func ProbeArgs(patch string, more ...string) []string {
	args := []string{"--profile", "sdk"}
	for _, p := range more {
		args = append(args, "--patch", p)
	}
	return append(args, "--patch", patch)
}

// ParseProbe reads the catalog from the probe's output: each provider's models, Value
// "<provider>/<model>" (Description, the provider's name before the model's own, when more than
// one offers models), and the default.
func ParseProbe(out []byte) (agent.Catalog, error) {
	var line []byte
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		if b, ok := bytes.CutPrefix(bytes.TrimSpace(sc.Bytes()), []byte(probeMark)); ok {
			line = bytes.Clone(b)
		}
	}
	if line == nil {
		return agent.Catalog{}, errors.New("dsh: the probe printed no models")
	}
	var p struct {
		Providers []struct {
			ID, Name string
			Models   []struct {
				ID, Name, Description string
				Efforts               []string
				DefaultEffort         string
			}
		}
		Default *struct{ Provider, Model string }
	}
	if err := json.Unmarshal(line, &p); err != nil {
		return agent.Catalog{}, fmt.Errorf("dsh: the probe's models: %w", err)
	}
	offering := 0
	for _, pr := range p.Providers {
		if len(pr.Models) > 0 {
			offering++
		}
	}
	var c agent.Catalog
	for _, pr := range p.Providers {
		for _, m := range pr.Models {
			if pr.ID == "" || m.ID == "" {
				continue
			}
			desc := m.Description
			if offering > 1 {
				desc = strings.TrimSuffix(pr.Name+" · "+desc, " · ")
			}
			c.Models = append(c.Models, agent.Model{Value: pr.ID + "/" + m.ID, Name: m.Name, Description: desc, Efforts: m.Efforts, NoEffort: len(m.Efforts) == 0})
		}
	}
	if d := p.Default; d != nil && d.Provider != "" && d.Model != "" {
		c.Default = d.Provider + "/" + d.Model
	}
	if len(c.Models) == 0 {
		return agent.Catalog{}, errors.New("dsh: no provider offers a model")
	}
	return c, nil
}

// Route is the provider and model a model value names: "<provider>/<model>" of a provider in
// the catalog (any "<provider>/<model>" when there is none); one without (set before models were
// dsh's) is the first provider's of that id, else the default's provider's, else LegacyProvider's.
func Route(value string, c agent.Catalog) (provider, model string) {
	for _, m := range c.Models {
		if p, id, ok := strings.Cut(m.Value, "/"); ok && m.Value == value {
			return p, id
		}
	}
	if p, id, ok := strings.Cut(value, "/"); ok {
		for _, m := range c.Models {
			if strings.HasPrefix(m.Value, p+"/") {
				return p, id
			}
		}
	}
	for _, m := range c.Models {
		if p, id, _ := strings.Cut(m.Value, "/"); id == value {
			return p, id
		}
	}
	// No catalog to tell: a value with a provider is one.
	if p, id, ok := strings.Cut(value, "/"); ok && (len(c.Models) == 0 || p == LegacyProvider) {
		return p, id
	}
	provider = LegacyProvider
	if p, _, ok := strings.Cut(c.Default, "/"); ok {
		provider = p
	}
	return provider, value
}

// modelIn is the catalog's model of value (a Route's), nil if it has none.
func modelIn(value string, c agent.Catalog) *agent.Model {
	p, id := Route(value, c)
	for i, m := range c.Models {
		if m.Value == p+"/"+id {
			return &c.Models[i]
		}
	}
	return nil
}
