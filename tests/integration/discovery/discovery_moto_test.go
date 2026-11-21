//go:build moto

// Package discovery is an integration test of AWS discovery against a LOCAL moto server that
// examples/migration/aws-discovery-demo/seed_moto.py has seeded (run-demo.sh does both).
//
//	HERMES_MOTO_ENDPOINT=http://127.0.0.1:5055 AWS_ACCESS_KEY_ID=<operator key from operator.env> ... \
//	  go test -tags moto ./tests/integration/discovery/...
//
// It refuses any endpoint that is not on localhost and isolates the AWS config files, so it can
// never reach real AWS.
package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
	awsinv "github.com/nityam02/hermes-cloud-foundation/internal/inventory/aws"
)

func motoAuth(t *testing.T, endpoint, count string) {
	t.Helper()
	resp, err := http.Post(endpoint+"/moto-api/reset-auth", "text/plain", strings.NewReader(count))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("moto reset-auth: %s", resp.Status)
	}
}

func discoverWithIAMEnforcement(t *testing.T, ctx context.Context, endpoint, region string) *inventory.Inventory {
	t.Helper()
	motoAuth(t, endpoint, "0")
	defer motoAuth(t, endpoint, "inf")
	cfg, err := awsinv.NewSession(ctx, awsinv.SessionOptions{Region: region, EndpointURL: endpoint, MaxAttempts: 1,
		RoleARN: "arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly", ExternalID: "demo-external-id"})
	if err != nil {
		t.Fatal(err)
	}
	d := &awsinv.Discoverer{Clients: awsinv.NewClientFactory(cfg), Options: awsinv.Options{HomeRegion: region, Regions: []string{region}}}
	inv, err := d.Run(ctx)
	if err != nil {
		t.Fatalf("discovery must continue when calls are denied: %v", err)
	}
	return inv
}

func TestDiscoveryAgainstMoto(t *testing.T) {
	endpoint := os.Getenv("HERMES_MOTO_ENDPOINT")
	if endpoint == "" {
		t.Skip("HERMES_MOTO_ENDPOINT not set (run examples/migration/aws-discovery-demo/run-demo.sh)")
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		t.Fatalf("refusing non-local endpoint %q", endpoint)
	}
	t.Setenv("AWS_CONFIG_FILE", os.DevNull)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", os.DevNull)
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	region := os.Getenv("HERMES_MOTO_REGION")
	if region == "" {
		region = "eu-north-1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rec := awsinv.NewRecorder()
	cfg, err := awsinv.NewSession(ctx, awsinv.SessionOptions{Region: region, EndpointURL: endpoint, Recorder: rec,
		RoleARN: "arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly", ExternalID: "demo-external-id"})
	if err != nil {
		t.Fatal(err)
	}
	d := &awsinv.Discoverer{Clients: awsinv.NewClientFactory(cfg), Options: awsinv.Options{HomeRegion: region, Regions: []string{region},
		RoleARN: "arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly", ExternalIDUsed: true}}
	inv, err := d.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := inv.Check(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inv.Metadata.Identity.PrincipalARN, "assumed-role/HermesDiscoveryReadOnly") {
		t.Errorf("discovery did not run as the assumed role: %s", inv.Metadata.Identity.PrincipalARN)
	}
	byType := inv.Summary.ByType
	for typ, min := range map[string]int{"AWS::EC2::VPC": 1, "AWS::EC2::Subnet": 4, "AWS::EC2::SecurityGroup": 4, "AWS::EC2::Instance": 3,
		"AWS::RDS::DBInstance": 1, "AWS::S3::Bucket": 3, "AWS::SNS::Topic": 1, "AWS::SQS::Queue": 4, "AWS::SecretsManager::Secret": 2,
		"AWS::DynamoDB::Table": 1, "AWS::ECS::Service": 1, "AWS::IAM::Role": 4, "AWS::KMS::Key": 1, "AWS::ElasticLoadBalancingV2::LoadBalancer": 1,
		"AWS::Lambda::Function": 1} {
		if byType[typ] < min {
			t.Errorf("%s: found %d, want >= %d", typ, byType[typ], min)
		}
	}
	var relay *inventory.Resource
	for i := range inv.Resources {
		if inv.Resources[i].Type == "AWS::EC2::Instance" && inv.Resources[i].Name == "vpn-relay" {
			relay = &inv.Resources[i]
		}
	}
	if relay == nil || relay.Attr("source_dest_check") != false {
		t.Errorf("VPN relay with source/dest check disabled not discovered: %+v", relay)
	}
	rels := map[string]int{}
	for _, r := range inv.Relationships {
		rels[r.Type]++
	}
	for _, typ := range []string{"in-vpc", "in-subnet", "uses-security-group", "allows-ingress-from", "subscribes-to", "depends-on", "encrypted-by", "uses-secret"} {
		if rels[typ] == 0 {
			t.Errorf("no %s relationships discovered (have %v)", typ, rels)
		}
	}
	enforced := discoverWithIAMEnforcement(t, ctx, endpoint, region)
	// The seed denies kinesis:ListStreams and s3:GetBucketPolicy for the discovery role. (moto's IAM
	// emulation also denies some operations whose action names it maps differently from AWS; those
	// gaps are expected noise and are not asserted.)
	denied := map[string]bool{}
	for _, g := range enforced.Gaps {
		if g.Reason == inventory.GapAccessDenied {
			denied[g.Operation] = true
		}
	}
	for _, op := range []string{"ListStreams", "GetBucketPolicy"} {
		if !denied[op] {
			t.Errorf("denied %s did not become an access-denied gap (gaps: %+v)", op, enforced.Gaps)
		}
	}
	partial := 0
	for _, r := range enforced.Resources {
		if r.Type == "AWS::S3::Bucket" && r.Completeness == inventory.CompletenessPartial {
			partial++
		}
	}
	if partial < 3 || len(enforced.Resources) == 0 {
		t.Errorf("denied detail calls must mark buckets partial and discovery must continue (partial=%d, resources=%d)", partial, len(enforced.Resources))
	}
	raw, _ := json.Marshal(inv)
	for _, leak := range []string{"demo-not-a-secret", "demo-env-value"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("inventory leaks %q", leak)
		}
	}
	for op := range rec.Operations() {
		parts := strings.SplitN(op, ":", 2)
		if ok, why := awsinv.Allowed(parts[0], parts[1]); !ok {
			t.Errorf("operation %s was sent but is not allowlisted: %s", op, why)
		}
	}
	t.Logf("discovered %d resources, %d relationships, %d gaps using %d distinct operations", len(inv.Resources), len(inv.Relationships), len(inv.Gaps), len(rec.Operations()))
}
