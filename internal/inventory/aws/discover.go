package awsinv

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

// Options configures a discovery run.
type Options struct {
	HomeRegion        string   // region for STS and global services (IAM, Route 53, CloudFront, S3 listing)
	Regions           []string // regions to scan; empty = enabled regions from ec2:DescribeRegions
	Services          []string // Hermes service names; empty = all
	Concurrency       int      // parallel collector tasks (default 4)
	CloudControlTypes []string // optional generic Cloud Control fallback types
	RoleARN           string
	ExternalIDUsed    bool
	Generator         string
	Now               func() time.Time
}

// Discoverer runs the collectors.
type Discoverer struct {
	Clients ClientFactory
	Options Options
}

type collector struct {
	service string
	global  bool
	run     func(ctx context.Context, s *scope)
}

func collectors() []collector {
	return []collector{
		{service: SvcEC2, run: collectEC2},
		{service: SvcELBv2, run: collectELBv2},
		{service: SvcRDS, run: collectRDS},
		{service: SvcS3, global: true, run: collectS3},
		{service: SvcECS, run: collectECS},
		{service: SvcEKS, run: collectEKS},
		{service: SvcLambda, run: collectLambda},
		{service: SvcECR, run: collectECR},
		{service: SvcSQS, run: collectSQS},
		{service: SvcSNS, run: collectSNS},
		{service: SvcDynamoDB, run: collectDynamoDB},
		{service: SvcElastiCache, run: collectElastiCache},
		{service: SvcKinesis, run: collectKinesis},
		{service: SvcRoute53, global: true, run: collectRoute53},
		{service: SvcACM, run: collectACM},
		{service: SvcIAM, global: true, run: collectIAM},
		{service: SvcSecretsManager, run: collectSecrets},
		{service: SvcSSM, run: collectSSM},
		{service: SvcKMS, run: collectKMS},
		{service: SvcLogs, run: collectLogs},
		{service: SvcCloudWatch, run: collectAlarms},
		{service: SvcCloudFront, global: true, run: collectCloudFront},
		{service: SvcAPIGatewayV2, run: collectAPIGatewayV2},
		{service: SvcSFN, run: collectSFN},
		{service: SvcEvents, run: collectEvents},
		{service: SvcCloudFormation, run: collectCloudFormation},
		{service: SvcCloudControl, run: collectCloudControl},
	}
}

// sink collects results from concurrent collectors.
type sink struct {
	mu        sync.Mutex
	resources []inventory.Resource
	rels      []inventory.Relationship
	gaps      []inventory.Gap
	partial   map[string]bool
	profiles  map[string][]string // instance profile ARN -> role ARNs
	stackRes  []inventory.StackResource
	notes     []string
}

type scope struct {
	region     string
	account    string
	partition  string
	home       bool
	c          *Clients
	sink       *sink
	ccTypes    []string
	regionSet  map[string]bool
	clientsFor func(region string) *Clients
}

func (s *scope) add(r inventory.Resource) {
	if r.Region == "" {
		r.Region = s.region
	}
	if r.Completeness == "" {
		r.Completeness = inventory.CompletenessFull
	}
	s.sink.mu.Lock()
	s.sink.resources = append(s.sink.resources, r)
	s.sink.mu.Unlock()
}

func (s *scope) rel(from, to, typ string) { s.relE(from, to, typ, inventory.EvidenceAPI) }

func (s *scope) relE(from, to, typ, evidence string) {
	if from == "" || to == "" {
		return
	}
	s.sink.mu.Lock()
	s.sink.rels = append(s.sink.rels, inventory.Relationship{From: from, To: to, Type: typ, Evidence: evidence})
	s.sink.mu.Unlock()
}

// gap records a failed call. When resourceID is set the resource is marked partial.
func (s *scope) gap(service, op string, err error, impact, resourceID string) {
	reason, msg := ClassifyError(err)
	s.sink.mu.Lock()
	defer s.sink.mu.Unlock()
	s.sink.gaps = append(s.sink.gaps, inventory.Gap{Service: service, Region: s.region, Operation: op, Reason: reason, Message: msg, Impact: impact, ResourceID: resourceID})
	if resourceID != "" {
		s.sink.partial[resourceID] = true
	}
}

func (s *scope) note(format string, args ...any) {
	s.sink.mu.Lock()
	s.sink.notes = append(s.sink.notes, fmt.Sprintf(format, args...))
	s.sink.mu.Unlock()
}

func (s *scope) arn(service, resource string) string {
	return fmt.Sprintf("arn:%s:%s:%s:%s:%s", s.partition, service, s.region, s.account, resource)
}

// Run executes discovery and returns a finalized inventory.
func (d *Discoverer) Run(ctx context.Context) (*inventory.Inventory, error) {
	o := d.Options
	if o.HomeRegion == "" {
		o.HomeRegion = "us-east-1"
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 4
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Generator == "" {
		o.Generator = "hermes"
	}
	selected, err := selectServices(o.Services)
	if err != nil {
		return nil, err
	}
	home := d.Clients(o.HomeRegion)

	ident, err := home.STS.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("aws: sts:GetCallerIdentity failed (check credentials, --profile or --role-arn/--external-id): %w", err)
	}
	account := aws.ToString(ident.Account)
	principal := aws.ToString(ident.Arn)
	partition := "aws"
	if parts := strings.SplitN(principal, ":", 3); len(parts) == 3 && parts[1] != "" {
		partition = parts[1]
	}

	regions := o.Regions
	if len(regions) == 0 {
		regions, err = enabledRegions(ctx, home.EC2)
		if err != nil {
			return nil, fmt.Errorf("aws: list enabled regions with ec2:DescribeRegions failed; pass --regions explicitly: %w", err)
		}
	}
	sort.Strings(regions)
	regionSet := map[string]bool{}
	for _, r := range regions {
		regionSet[r] = true
	}

	sk := &sink{partial: map[string]bool{}, profiles: map[string][]string{}}
	var clientMu sync.Mutex
	clientCache := map[string]*Clients{o.HomeRegion: home}
	clientsFor := func(region string) *Clients {
		clientMu.Lock()
		defer clientMu.Unlock()
		if c, ok := clientCache[region]; ok {
			return c
		}
		c := d.Clients(region)
		clientCache[region] = c
		return c
	}

	type task struct {
		col    collector
		region string
	}
	var tasks []task
	for _, col := range collectors() {
		if !selected[col.service] {
			continue
		}
		if col.service == SvcCloudControl && len(o.CloudControlTypes) == 0 {
			continue
		}
		if col.global {
			tasks = append(tasks, task{col, o.HomeRegion})
			continue
		}
		for _, r := range regions {
			tasks = append(tasks, task{col, r})
		}
	}

	ch := make(chan task)
	var wg sync.WaitGroup
	for i := 0; i < o.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range ch {
				if ctx.Err() != nil {
					continue
				}
				s := &scope{region: t.region, account: account, partition: partition, home: t.region == o.HomeRegion,
					c: clientsFor(t.region), sink: sk, ccTypes: o.CloudControlTypes, regionSet: regionSet, clientsFor: clientsFor}
				t.col.run(ctx, s)
			}
		}()
	}
	for _, t := range tasks {
		ch <- t
	}
	close(ch)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("aws: discovery interrupted: %w", err)
	}

	inv := inventory.New("aws", inventory.CollectorAWSAPI)
	inv.Metadata.GeneratedAt = o.Now().UTC().Format(time.RFC3339)
	inv.Metadata.Generator = o.Generator
	inv.Metadata.AccountID = account
	inv.Metadata.Regions = regions
	inv.Metadata.Identity = &inventory.Identity{PrincipalARN: principal, RoleARN: o.RoleARN, ExternalIDUsed: o.ExternalIDUsed}
	for svc := range selected {
		inv.Metadata.Services = append(inv.Metadata.Services, svc)
	}
	sort.Strings(inv.Metadata.Services)
	sort.Strings(sk.notes)
	inv.Metadata.Notes = sk.notes
	inv.Resources = sk.resources
	inv.Relationships = sk.rels
	inv.Gaps = sk.gaps
	for i := range inv.Resources {
		if sk.partial[inv.Resources[i].ID] && inv.Resources[i].Completeness == inventory.CompletenessFull {
			inv.Resources[i].Completeness = inventory.CompletenessPartial
		}
	}
	resolveReferences(inv, sk.profiles)
	inventory.AnnotateFromTags(inv)
	inventory.AnnotateFromStacks(inv, sk.stackRes)
	inv.Finalize()
	return inv, nil
}

func selectServices(names []string) (map[string]bool, error) {
	all := ServiceNames()
	sel := map[string]bool{}
	if len(names) == 0 {
		for _, n := range all {
			sel[n] = true
		}
		return sel, nil
	}
	valid := map[string]bool{}
	for _, n := range all {
		valid[n] = true
	}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" {
			continue
		}
		if !valid[n] {
			return nil, fmt.Errorf("unknown service %q (valid: %s)", n, strings.Join(all, ", "))
		}
		sel[n] = true
	}
	return sel, nil
}

func enabledRegions(ctx context.Context, c EC2API) ([]string, error) {
	out, err := c.DescribeRegions(ctx, &ec2.DescribeRegionsInput{AllRegions: aws.Bool(true)})
	if err != nil {
		return nil, err
	}
	var regions []string
	for _, r := range out.Regions {
		switch aws.ToString(r.OptInStatus) {
		case "opt-in-not-required", "opted-in", "":
			regions = append(regions, aws.ToString(r.RegionName))
		}
	}
	if len(regions) == 0 {
		return nil, fmt.Errorf("no enabled regions returned")
	}
	return regions, nil
}

// resolveReferences rewrites relationship targets that collectors could only express as
// aliases (KMS aliases and key ids, partial Secrets Manager ARNs, instance profiles, load
// balancer DNS names) into inventory ids, and drops inferred edges to unknown resources.
func resolveReferences(inv *inventory.Inventory, profiles map[string][]string) {
	ids := map[string]bool{}
	alias := map[string]string{}
	for _, r := range inv.Resources {
		ids[r.ID] = true
		switch r.Type {
		case "AWS::KMS::Key":
			if kid := r.AttrString("key_id"); kid != "" {
				alias[kid] = r.ID
				if i := strings.Index(r.ID, ":key/"); i > 0 {
					alias[r.ID[:i]+":key/"+kid] = r.ID
				}
			}
			for _, a := range r.AttrStrings("alias_arns") {
				alias[a] = r.ID
			}
		case "AWS::SecretsManager::Secret":
			// Secrets Manager appends "-" and six random characters to secret ARNs; references
			// may omit them.
			if len(r.ID) > 7 && r.ID[len(r.ID)-7] == '-' {
				alias[r.ID[:len(r.ID)-7]] = r.ID
			}
		case "AWS::ElasticLoadBalancingV2::LoadBalancer":
			if dns := r.AttrString("dns_name"); dns != "" {
				alias["dns:"+strings.ToLower(dns)] = r.ID
			}
		}
	}
	var out []inventory.Relationship
	for _, rel := range inv.Relationships {
		if roles, ok := profiles[rel.To]; ok && rel.Type == inventory.RelAssumesRole {
			for _, role := range roles {
				out = append(out, inventory.Relationship{From: rel.From, To: role, Type: rel.Type, Evidence: rel.Evidence})
			}
			continue
		}
		if !ids[rel.To] {
			if a, ok := alias[rel.To]; ok {
				rel.To = a
			}
		}
		if !ids[rel.To] && (rel.Evidence == inventory.EvidenceInferred || strings.HasPrefix(rel.To, "dns:")) {
			continue
		}
		out = append(out, rel)
	}
	inv.Relationships = out
}
