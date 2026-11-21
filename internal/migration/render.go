package migration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func mdEscape(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}

func humanBytes(b int64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "kMGTPE"[exp])
}

// AssessmentMarkdown renders a human-readable assessment.
func AssessmentMarkdown(a *Assessment) string {
	var b strings.Builder
	s := a.Summary
	fmt.Fprintf(&b, "# Migration assessment: %s → %s\n\n", a.Metadata.Source.Provider, a.Metadata.Target.Provider)
	fmt.Fprintf(&b, "> %s\n\n", a.Metadata.Disclaimer)
	fmt.Fprintf(&b, "Generated %s by %s from inventory `%s` (collector `%s`).\n\n", a.Metadata.GeneratedAt, a.Metadata.Generator, short(strings.TrimPrefix(a.Metadata.InventoryDigest, "sha256:")), a.Metadata.InventoryCollector)
	b.WriteString("## Summary\n\n| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| Resources | %d |\n| Portability score | **%.1f%%** |\n| Blockers / risks | %d / %d |\n| Discovery gaps | %d |\n| Dependency waves | %d |\n",
		s.ResourceCount, s.PortabilityScore, s.BlockerCount, s.RiskCount, s.GapCount, s.WaveCount)
	fmt.Fprintf(&b, "| Data to migrate | %s required, %s optional (%d data-bearing resource(s) without size) |\n",
		humanBytes(s.DataVolume.RequiredBytes), humanBytes(s.DataVolume.OptionalBytes), len(s.DataVolume.UnknownSizeResources))
	fmt.Fprintf(&b, "| Indicative transfer cost | USD %.2f – %.2f (range, not a quote) |\n\n", s.TransferCost.LowUSD, s.TransferCost.HighUSD)
	fmt.Fprintf(&b, "Portability score method: %s.\n\n", s.PortabilityMethod)
	b.WriteString("### By compatibility\n\n")
	for _, k := range sortedKeysInt(s.ByCompatibility) {
		fmt.Fprintf(&b, "- %s: %d\n", k, s.ByCompatibility[k])
	}
	b.WriteString("\n### Transfer cost\n\n")
	fmt.Fprintf(&b, "%s\n\n| Rate id | Provider | Kind | USD/GB | Applies to | Source |\n|---|---|---|---|---|---|\n", s.TransferCost.Method)
	for _, r := range s.TransferCost.RatesUsed {
		fmt.Fprintf(&b, "| %s | %s | %s | %.4f | %s | %s |\n", r.ID, r.Provider, r.Kind, r.USDPerGB, mdEscape(r.AppliesTo), r.Source)
	}
	if len(s.TransferCost.ExitProgrammes) > 0 {
		b.WriteString("\nExit-fee waiver programmes (may apply; not subtracted):\n\n")
		for _, n := range s.TransferCost.ExitProgrammes {
			fmt.Fprintf(&b, "- %s\n", strings.TrimPrefix(n, "May apply (not subtracted): "))
		}
	}
	b.WriteString("\n## Blockers, risks and notes\n\n| Severity | Code | Finding | Recommendation | Resources |\n|---|---|---|---|---|\n")
	for _, bl := range a.Blockers {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d |\n", bl.Severity, bl.Code, mdEscape(bl.Message), mdEscape(bl.Recommendation), len(bl.ResourceIDs))
	}
	b.WriteString("\n## Discovery gaps (undiscovered scope is risk)\n\n")
	if len(a.Gaps) == 0 {
		b.WriteString("None.\n")
	} else {
		b.WriteString("| Service | Region | Operation | Reason | Impact |\n|---|---|---|---|---|\n")
		for _, g := range a.Gaps {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", g.Service, g.Region, g.Operation, g.Reason, mdEscape(g.Impact))
		}
	}
	b.WriteString("\n## Migration waves\n\n")
	for _, w := range a.Dependencies.Waves {
		fmt.Fprintf(&b, "- Wave %d: %d resource(s)", w.Index, len(w.Resources))
		if len(w.Cycles) > 0 {
			fmt.Fprintf(&b, " (%d dependency cycle(s) collapsed)", len(w.Cycles))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n## Resources\n\n| Type | Name | Category | Target service | Compatibility | Complexity | Data | Hermes | Wave | Workflow |\n|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range a.Resources {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %d | %s |\n", r.Type, mdEscape(r.Name), r.Category, mdEscape(r.TargetService), r.Compatibility, r.Complexity, r.DataMigration, r.HermesSupport, r.Wave, r.Workflow)
	}
	b.WriteString("\n## Draft target spec\n\nThe assessment contains `draft_spec`, a DRAFT Platform document that requires human review. Write it out with `hermes migrate generate`.\n")
	return b.String()
}

func sortedKeysInt(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PlanMarkdown renders the plan as a runbook-style document.
func PlanMarkdown(p *Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Migration plan %s\n\n", short(p.Metadata.PlanID))
	fmt.Fprintf(&b, "Source: %s %s %v → target: %s (environment `%s`). Plan id `%s`.\n\n", p.Metadata.Source.Provider, p.Metadata.Source.AccountID, p.Metadata.Source.Regions, p.Metadata.Target.Provider, p.Metadata.Target.Environment, p.Metadata.PlanID)
	fmt.Fprintf(&b, "%d steps in %d phases: %d approval gates, %d validation gates, %d steps that touch the source, %d manual steps.\n\n",
		p.Summary.Steps, p.Summary.Phases, p.Summary.ApprovalGates, p.Summary.ValidationGates, p.Summary.SourceImpactingSteps, p.Summary.ManualSteps)
	b.WriteString("Hermes never modifies or deletes source resources by itself. Steps marked *source impact* change the source and need explicit approval; decommission is always manual.\n\n")
	for _, ph := range p.Phases {
		fmt.Fprintf(&b, "## %d. %s\n\n| Step | Title | Automation | Gate | Source impact | Reversible | Depends on | Resources |\n|---|---|---|---|---|---|---|---|\n", ph.Order, ph.ID)
		for _, s := range ph.Steps {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %v | %d | %d |\n", s.ID, mdEscape(s.Title), s.Automation, s.Gate, s.SourceImpact, s.Reversible, len(s.DependsOn), len(s.ResourceRefs))
		}
		b.WriteString("\n")
	}
	if len(p.Notes) > 0 {
		b.WriteString("## Notes\n\n")
		for _, n := range p.Notes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
	}
	return b.String()
}

// GenerateResult lists the files written by Generate.
type GenerateResult struct {
	Dir   string   `json:"dir"`
	Files []string `json:"files"`
}

const draftHeader = "# DRAFT — requires human review.\n" +
	"# Generated by `hermes migrate generate` from assessment %s (target %s).\n" +
	"# Review every value against MAPPING.md and UNSUPPORTED.md, then run:\n" +
	"#   hermes validate -c %s\n" +
	"#   hermes render -c %s --env %s     # renders OpenTofu with the platform renderer\n" +
	"#   hermes plan -c %s --env %s       # destination provisioning only after review and approval\n"

// Generate writes <dir>/hermes.yaml, MAPPING.md and UNSUPPORTED.md from an assessment.
func Generate(a *Assessment, dir, project, env string) (*GenerateResult, error) {
	var spec DraftPlatform
	raw, _ := json.Marshal(a.DraftSpec)
	if err := json.Unmarshal(raw, &spec); err != nil || spec.Kind != "Platform" {
		return nil, fmt.Errorf("generate: assessment has no valid draft_spec")
	}
	if project != "" {
		if !projectRe.MatchString(project) {
			return nil, fmt.Errorf("generate: project %q must match %s", project, projectRe)
		}
		spec.Project.Name = project
	}
	if env != "" {
		if !envRe.MatchString(env) {
			return nil, fmt.Errorf("generate: environment %q must match %s", env, envRe)
		}
		spec.Project.Environment = env
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}
	specPath := filepath.Join(dir, "hermes.yaml")
	var buf bytes.Buffer
	e := spec.Project.Environment
	fmt.Fprintf(&buf, draftHeader, short(strings.TrimPrefix(a.Digest(), "sha256:")), spec.Cloud.Provider, specPath, specPath, e, specPath, e)
	if spec.Cloud.Account == "" {
		buf.WriteString("# cloud.account is empty: set the target project/account id before planning.\n")
	}
	images := replaceSourceRegistryImages(&spec)
	var reviews []string
	for _, m := range a.SpecMapping {
		if m.Review && m.TargetKind != "absorbed" {
			reviews = append(reviews, fmt.Sprintf("#   - %s %s (from %s): %s", m.TargetKind, m.TargetName, m.SourceName, m.Note))
		}
	}
	for _, im := range images {
		reviews = append(reviews, fmt.Sprintf("#   - service %s: image %s is in the source registry; copy it to the target registry and replace %s (see MAPPING.md)", im.Service, im.Source, im.Placeholder))
	}
	if len(reviews) > 0 {
		buf.WriteString("# Items flagged for review:\n" + strings.Join(reviews, "\n") + "\n")
	}
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(spec); err != nil {
		return nil, err
	}
	_ = enc.Close()
	files := map[string][]byte{
		"hermes.yaml":    buf.Bytes(),
		"MAPPING.md":     []byte(mappingMarkdown(a, spec) + imagesMarkdown(images)),
		"UNSUPPORTED.md": []byte(unsupportedMarkdown(a, spec.Cloud.Provider)),
	}
	res := &GenerateResult{Dir: dir}
	for _, name := range []string{"hermes.yaml", "MAPPING.md", "UNSUPPORTED.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), files[name], 0o644); err != nil { //nolint:gosec // generated spec and docs meant to be reviewed and committed
			return nil, fmt.Errorf("generate: %w", err)
		}
		res.Files = append(res.Files, filepath.Join(dir, name))
	}
	return res, nil
}

// ImageCopy is a container image the target cannot keep pulling from the source registry.
type ImageCopy struct {
	Service     string
	Source      string
	Placeholder string
}

// PlaceholderRegistry replaces a source-registry host in generated specs.
const PlaceholderRegistry = "REPLACE-WITH-TARGET-REGISTRY"

// sourceRegistryRe matches Amazon ECR image references (account.dkr.ecr.region.amazonaws.com[.cn]/repo...).
var sourceRegistryRe = regexp.MustCompile(`^[0-9]{12}\.dkr\.ecr(-fips)?\.[a-z0-9-]+\.amazonaws\.com(\.cn)?/(.+)$`)

// replaceSourceRegistryImages swaps every source-registry (ECR) image in the draft for
// REPLACE-WITH-TARGET-REGISTRY/<repo>:<tag> (F-17): a target that keeps pulling from the
// source registry blocks decommissioning, and Cloud Run / SAE cannot pull from ECR. The images
// to copy are returned for MAPPING.md and the review list.
func replaceSourceRegistryImages(spec *DraftPlatform) []ImageCopy {
	if spec.Workloads.Compute == nil {
		return nil
	}
	var out []ImageCopy
	for i := range spec.Workloads.Compute.Services {
		svc := &spec.Workloads.Compute.Services[i]
		m := sourceRegistryRe.FindStringSubmatch(svc.Image)
		if m == nil {
			continue
		}
		placeholder := PlaceholderRegistry + "/" + m[3]
		out = append(out, ImageCopy{Service: svc.Name, Source: svc.Image, Placeholder: placeholder})
		svc.Image = placeholder
	}
	return out
}

func imagesMarkdown(images []ImageCopy) string {
	if len(images) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Container images to copy to the target registry\n\n")
	b.WriteString("These images live in the source registry (Amazon ECR). The target must not pull from it: copy each image (for example with `crane copy` or `skopeo copy`) to a registry the target can read, then replace the placeholder host `" + PlaceholderRegistry + "` in `hermes.yaml`. Pin digests in production.\n\n")
	b.WriteString("| Service | Source image | Placeholder in hermes.yaml |\n|---|---|---|\n")
	for _, im := range images {
		fmt.Fprintf(&b, "| %s | `%s` | `%s` |\n", im.Service, im.Source, im.Placeholder)
	}
	return b.String()
}

func mappingMarkdown(a *Assessment, spec DraftPlatform) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Source → target mapping (DRAFT — requires human review)\n\nTarget: %s, region `%s`, project `%s`, environment `%s`.\n\n", spec.Cloud.Provider, spec.Cloud.Region, spec.Project.Name, spec.Project.Environment)
	b.WriteString("Native target names follow `<project>-<environment>-<name>` (buckets also get a random suffix); the names below are spec names.\n\n")
	b.WriteString("| Source type | Source name | Target | Spec name | Review | Notes |\n|---|---|---|---|---|---|\n")
	for _, m := range a.SpecMapping {
		review := ""
		if m.Review {
			review = "yes"
		}
		fmt.Fprintf(&b, "| %s | %s | `%s` | %s | %s | %s |\n", m.SourceType, mdEscape(m.SourceName), m.TargetPath, m.TargetName, review, mdEscape(m.Note))
	}
	return b.String()
}

func unsupportedMarkdown(a *Assessment, target string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Resources Hermes cannot generate on %s\n\n", target)
	b.WriteString("These source resources are outside the Hermes v1alpha1 provisioning scope or need a decision. Provision them with the recommended service (registry module or provider resource names are search starting points; verify them), keep them on the source with hybrid connectivity, or retire them.\n\n")
	if len(a.Unsupported) == 0 {
		b.WriteString("None.\n")
		return b.String()
	}
	b.WriteString("| Category | Source type | Source name | Reason | Recommended target service | Suggested module / resource |\n|---|---|---|---|---|---|\n")
	for _, u := range a.Unsupported {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", u.Category, u.SourceType, mdEscape(u.SourceName), mdEscape(u.Reason), mdEscape(u.RecommendedService), mdEscape(u.Suggestion))
	}
	return b.String()
}

// CatalogMarkdown renders the catalog as a deterministic table grouped by category.
func CatalogMarkdown(c *Catalog, targets []string) string {
	var b strings.Builder
	b.WriteString("<!-- Generated by `hermes migrate catalog --format markdown`; do not edit. -->\n")
	b.WriteString("# Service catalog\n\n")
	fmt.Fprintf(&b, "Reviewed %s. %s\n\n", c.Metadata.ReviewedAt, c.Metadata.Disclaimer)
	byCat := map[string][]CatalogEntry{}
	for _, e := range c.Entries {
		byCat[e.Category] = append(byCat[e.Category], e)
	}
	var cats []string
	for k := range byCat {
		cats = append(cats, k)
	}
	sort.Strings(cats)
	for _, cat := range cats {
		es := byCat[cat]
		sort.Slice(es, func(i, j int) bool { return es[i].ID < es[j].ID })
		fmt.Fprintf(&b, "## %s\n\n", cat)
		b.WriteString("| Source service | CFN types | Target | Target service | Compatibility | Complexity | Data migration | Hermes support | Lock-in |\n|---|---|---|---|---|---|---|---|---|\n")
		for _, e := range es {
			types := strings.Join(e.Source.ResourceTypes, ", ")
			if len(e.Source.Match) > 0 {
				var keys []string
				for k := range e.Source.Match {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				var parts []string
				for _, k := range keys {
					var vals []string
					for _, v := range e.Source.Match[k] {
						vals = append(vals, Stringify(v))
					}
					parts = append(parts, k+"="+strings.Join(vals, "/"))
				}
				types += " (" + strings.Join(parts, ", ") + ")"
			}
			for _, t := range targets {
				tg := e.Targets[t]
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", mdEscape(e.Source.Service), mdEscape(types), t, mdEscape(tg.Service), tg.Compatibility, tg.Complexity, tg.DataMigration, tg.HermesSupport, tg.LockIn)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// CatalogText renders a compact text listing.
func CatalogText(c *Catalog, targets []string) string {
	var b strings.Builder
	for _, e := range c.Entries {
		for _, t := range targets {
			tg := e.Targets[t]
			fmt.Fprintf(&b, "%-34s %-7s %-24s %-12s %-14s %s\n", e.ID, t, e.Category, tg.Compatibility, tg.HermesSupport, tg.Service)
		}
	}
	return b.String()
}
