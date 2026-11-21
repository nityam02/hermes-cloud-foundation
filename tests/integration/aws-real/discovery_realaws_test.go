//go:build realaws

// Package awsreal is an OPT-IN, read-only discovery test against a real AWS account. It is
// never run by CI or by the Hermes developers' tooling. It runs only when
// HERMES_IT_ALLOW_REAL_AWS=1 and a dedicated discovery role is given:
//
//	HERMES_IT_ALLOW_REAL_AWS=1 HERMES_IT_AWS_ROLE_ARN=arn:aws:iam::<account>:role/HermesDiscoveryReadOnly \
//	HERMES_IT_AWS_EXTERNAL_ID=<id> HERMES_IT_AWS_REGION=eu-north-1 go test -tags realaws ./tests/integration/aws-real/
//
// Every call passes the read-only guard (Describe/List/Get allowlist, secret and data reads
// denied before sending); the role's policy (migration/discovery) denies them again in IAM.
package awsreal

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	awsinv "github.com/nityam02/hermes-cloud-foundation/internal/inventory/aws"
)

func TestReadOnlyDiscoveryRealAccount(t *testing.T) {
	if os.Getenv("HERMES_IT_ALLOW_REAL_AWS") != "1" {
		t.Skip("set HERMES_IT_ALLOW_REAL_AWS=1 to run against a real AWS account (read-only)")
	}
	role, ext := os.Getenv("HERMES_IT_AWS_ROLE_ARN"), os.Getenv("HERMES_IT_AWS_EXTERNAL_ID")
	if role == "" || ext == "" {
		t.Fatal("HERMES_IT_AWS_ROLE_ARN and HERMES_IT_AWS_EXTERNAL_ID are required (use the dedicated read-only discovery role)")
	}
	region := os.Getenv("HERMES_IT_AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	rec := awsinv.NewRecorder()
	cfg, err := awsinv.NewSession(ctx, awsinv.SessionOptions{Region: region, RoleARN: role, ExternalID: ext, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	d := &awsinv.Discoverer{Clients: awsinv.NewClientFactory(cfg), Options: awsinv.Options{HomeRegion: region, Regions: []string{region}, RoleARN: role, ExternalIDUsed: true}}
	inv, err := d.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !inv.Metadata.ReadOnly || !strings.Contains(inv.Metadata.Identity.PrincipalARN, ":assumed-role/") {
		t.Fatalf("discovery did not run as the assumed read-only role: %+v", inv.Metadata.Identity)
	}
	for op := range rec.Operations() {
		parts := strings.SplitN(op, ":", 2)
		if ok, why := awsinv.Allowed(parts[0], parts[1]); !ok {
			t.Errorf("sent a non-allowlisted operation %s: %s", op, why)
		}
	}
	for _, g := range inv.Gaps {
		t.Logf("gap: %s %s %s: %s", g.Service, g.Region, g.Operation, g.Reason)
	}
	b, _ := json.Marshal(inv.Summary)
	t.Logf("summary: %s; %d distinct operations", b, len(rec.Operations()))
	if out := os.Getenv("HERMES_IT_OUT"); out != "" {
		data, _ := inv.Marshal()
		if err := os.WriteFile(out, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
