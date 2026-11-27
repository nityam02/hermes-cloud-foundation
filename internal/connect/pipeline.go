package connect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
	awsinv "github.com/nityam02/hermes-cloud-foundation/internal/inventory/aws"
	"github.com/nityam02/hermes-cloud-foundation/internal/migration"
)

// NewExternalID returns a random external ID. Hermes generates it, not the customer (AWS guidance):
// it ties one role to one Hermes session and is not a secret.
func NewExternalID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing means the system cannot run safely
	}
	return "hermes-" + hex.EncodeToString(b)
}

// RoleTemplate is the CloudFormation template that creates the read-only discovery role.
func RoleTemplate() []byte { return awsinv.DiscoveryRoleTemplate() }

// CreateRoleCommand is the AWS CLI command that creates the role stack from a saved template file.
func CreateRoleCommand(templatePath, externalID, principalARN, region string) string {
	return fmt.Sprintf("aws cloudformation create-stack --stack-name hermes-discovery-role --region %s \\\n  --template-body file://%s \\\n  --capabilities CAPABILITY_NAMED_IAM \\\n  --parameters ParameterKey=ExternalId,ParameterValue=%s ParameterKey=TrustedPrincipalArn,ParameterValue=%s",
		region, templatePath, externalID, principalARN)
}

// Reporter receives progress from a long operation. status is running, done, failed or warn.
type Reporter func(step, status, detail string)

// PipelineRequest is everything the discovery job needs.
type PipelineRequest struct {
	AWS     AWSRequest `json:"aws"`
	Regions []string   `json:"regions"`
	Target  string     `json:"target"` // gcp
	Env     string     `json:"env"`
}

// Output holds the documents a pipeline run produced, as JSON, plus where they were saved.
type Output struct {
	Inventory  []byte
	Assessment []byte
	Plan       []byte
	Dir        string
}

// RunPipeline discovers the account, assesses the inventory and builds the migration plan. Everything
// is read-only on the source account. Documents are saved under outDir (0700, files 0600) so the CLI
// can use the same files.
func RunPipeline(ctx context.Context, req PipelineRequest, outDir, version string, discover func(context.Context, AWSRequest, []string) (*inventory.Inventory, error), report Reporter) (*Output, error) {
	if req.Target == "" {
		req.Target = "gcp"
	}
	if req.Target != "gcp" && req.Target != "alibaba" {
		return nil, fmt.Errorf("target must be gcp or alibaba")
	}
	if req.Env == "" {
		req.Env = "production"
	}
	if len(req.Regions) == 0 {
		req.Regions = []string{req.AWS.Region}
	}
	dir := filepath.Join(outDir, time.Now().UTC().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	out := &Output{Dir: dir}
	save := func(name string, data []byte) error { return os.WriteFile(filepath.Join(dir, name), data, 0o600) }

	report("discover", "running", fmt.Sprintf("Reading %d region(s) through the read-only role.", len(req.Regions)))
	inv, err := discover(ctx, req.AWS, req.Regions)
	if err != nil {
		report("discover", "failed", err.Error())
		return nil, fmt.Errorf("discovery: %w", err)
	}
	if out.Inventory, err = json.MarshalIndent(inv, "", "  "); err != nil {
		return nil, err
	}
	if err := save("inventory.json", out.Inventory); err != nil {
		return nil, err
	}
	report("discover", "done", fmt.Sprintf("Found %d resources and %d relationships; %d read calls were denied or unsupported.", len(inv.Resources), len(inv.Relationships), len(inv.Gaps)))

	report("assess", "running", "Comparing each resource with its closest "+req.Target+" service.")
	cat, err := migration.LoadCatalog("")
	if err != nil {
		report("assess", "failed", err.Error())
		return nil, err
	}
	pricing, err := migration.LoadPricing("")
	if err != nil {
		report("assess", "failed", err.Error())
		return nil, err
	}
	a, err := migration.Assess(inv, cat, pricing, migration.AssessOptions{Target: req.Target, Generator: "hermes-ui/" + version, Env: req.Env, Now: time.Now})
	if err != nil {
		report("assess", "failed", err.Error())
		return nil, fmt.Errorf("assessment: %w", err)
	}
	if out.Assessment, err = json.MarshalIndent(a, "", "  "); err != nil {
		return nil, err
	}
	if err := save("assessment.json", out.Assessment); err != nil {
		return nil, err
	}
	report("assess", "done", fmt.Sprintf("Portability %.0f%%: %d blockers, %d risks.", a.Summary.PortabilityScore, a.Summary.BlockerCount, a.Summary.RiskCount))

	report("plan", "running", "Ordering the migration into phases, steps and approval gates.")
	ws, err := migration.LoadWorkflows("")
	if err != nil {
		report("plan", "failed", err.Error())
		return nil, err
	}
	p, err := migration.BuildPlan(a, ws, migration.PlanOptions{Environment: req.Env, Generator: "hermes-ui/" + version, Now: time.Now})
	if err != nil {
		report("plan", "failed", err.Error())
		return nil, fmt.Errorf("plan: %w", err)
	}
	if out.Plan, err = json.Marshal(p); err != nil {
		return nil, err
	}
	y, err := p.YAML()
	if err != nil {
		return nil, err
	}
	if err := save("plan.yaml", y); err != nil {
		return nil, err
	}
	report("plan", "done", fmt.Sprintf("%d steps in %d phases; %d need a person to approve them.", p.Summary.Steps, p.Summary.Phases, p.Summary.ApprovalGates))
	return out, nil
}
