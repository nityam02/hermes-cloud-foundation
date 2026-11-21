package inventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinalizeDeterministicAndDeduplicated(t *testing.T) {
	inv := New("aws", CollectorFixture)
	inv.Resources = []Resource{
		{ID: "b", Type: "T"}, {ID: "a", Type: "T", Attributes: map[string]any{"x": 1}},
		{ID: "a", Type: "T", Completeness: CompletenessPartial, Attributes: map[string]any{"y": 2}},
	}
	inv.Relationships = []Relationship{{From: "a", To: "b", Type: RelDependsOn, Evidence: EvidenceInferred}, {From: "a", To: "b", Type: RelDependsOn}, {From: "a", To: "a", Type: RelDependsOn}, {From: "zz", To: "a", Type: RelDependsOn}}
	inv.Gaps = []Gap{{Service: "s3", Operation: "X"}, {Service: "s3", Operation: "X"}}
	inv.Metadata.Regions = []string{"b", "a", "a"}
	inv.Finalize()
	if len(inv.Resources) != 2 || inv.Resources[0].ID != "a" || inv.Resources[0].Completeness != CompletenessPartial || inv.Resources[0].Attributes["y"] != 2 {
		t.Fatalf("resources: %+v", inv.Resources)
	}
	if len(inv.Relationships) != 1 || inv.Relationships[0].Evidence != EvidenceAPI {
		t.Fatalf("relationships: %+v", inv.Relationships)
	}
	if len(inv.Gaps) != 1 || strings.Join(inv.Metadata.Regions, ",") != "a,b" || inv.Summary.ResourceCount != 2 || !inv.Metadata.ReadOnly {
		t.Fatalf("finalize: %+v", inv)
	}
	d1 := inv.Digest()
	inv.Finalize()
	if inv.Digest() != d1 {
		t.Error("finalize is not idempotent")
	}
}

func TestDecodeRejectsNonReadOnlyAndUnknownFields(t *testing.T) {
	if _, err := Decode(strings.NewReader(`{"apiVersion":"hermes/v1alpha1","kind":"Inventory","metadata":{"read_only":false},"resources":[],"relationships":[],"gaps":[],"summary":{"resource_count":0,"by_type":{},"gap_count":0}}`)); err == nil {
		t.Error("read_only=false must be rejected")
	}
	if _, err := Decode(strings.NewReader(`{"apiVersion":"hermes/v1alpha1","kind":"Inventory","bogus":1}`)); err == nil {
		t.Error("unknown fields must be rejected")
	}
}

func TestRedactAndSanitize(t *testing.T) {
	inv := New("aws", CollectorFixture)
	inv.Metadata.AccountID = "123456789012"
	inv.Metadata.Identity = &Identity{PrincipalARN: "arn:aws:sts::123456789012:assumed-role/R/s"}
	inv.Resources = []Resource{{ID: "arn:aws:iam::123456789012:role/x", Type: "AWS::IAM::Role", Attributes: map[string]any{
		"trust_aws_principals": []string{"arn:aws:iam::210987654321:root", "210987654321"}, "size": int64(214748364800)}}}
	Redact(inv)
	b, _ := json.Marshal(inv)
	if strings.Contains(string(b), "123456789012") || strings.Contains(string(b), "210987654321") || !strings.Contains(string(b), "214748364800") {
		t.Fatalf("redaction: %s", b)
	}
	clean := SanitizeProperties(map[string]any{"Name": "a", "MasterUserPassword": "x", "Nested": map[string]any{"AuthToken": "y", "Ok": 1}, "List": []any{map[string]any{"ClientSecret": "z"}}})
	cb, _ := json.Marshal(clean)
	if strings.Contains(string(cb), `"x"`) || strings.Contains(string(cb), `"y"`) || strings.Contains(string(cb), `"z"`) || !strings.Contains(string(cb), `"Ok"`) {
		t.Fatalf("sanitize: %s", cb)
	}
	// F-06: values under value-bearing keys (env vars, user data) are dropped whatever the
	// variable is called, e.g. an AWS Config item of a Lambda function or launch template.
	clean = SanitizeProperties(map[string]any{
		"functionName":         "api",
		"environment":          map[string]any{"variables": map[string]any{"DATABASE_URL": "postgres://app:CANARY-env@db"}},
		"LaunchTemplateData":   map[string]any{"UserData": "CANARY-userdata", "InstanceType": "t3.small"},
		"ContainerDefinitions": []any{map[string]any{"Name": "api", "Environment": []any{map[string]any{"Name": "K", "Value": "CANARY-ecs"}}}},
		"EnvironmentName":      "kept-not-a-value-bag",
	})
	cb, _ = json.Marshal(clean)
	if strings.Contains(string(cb), "CANARY") || !strings.Contains(string(cb), "t3.small") || !strings.Contains(string(cb), "kept-not-a-value-bag") {
		t.Fatalf("value-bearing keys must be dropped, other keys kept: %s", cb)
	}
}

func TestManagedByAnnotations(t *testing.T) {
	inv := New("aws", CollectorFixture)
	inv.Resources = []Resource{
		{ID: "arn:aws:s3:::logs", Type: "AWS::S3::Bucket", Name: "logs"},
		{ID: "arn:aws:sqs:eu-north-1:123456789012:jobs", Type: "AWS::SQS::Queue", Name: "jobs", Attributes: map[string]any{"queue_url": "https://sqs.eu-north-1.amazonaws.com/123456789012/jobs"}},
		{ID: "arn:aws:ec2:eu-north-1:123456789012:vpc/vpc-1", Type: "AWS::EC2::VPC", Attributes: map[string]any{"resource_id": "vpc-1"}},
		{ID: "arn:aws:sns:eu-north-1:123456789012:t", Type: "AWS::SNS::Topic", Tags: map[string]string{CloudFormationStackTag: "app"}},
	}
	state := `{"version":4,"resources":[{"mode":"managed","type":"aws_s3_bucket","name":"logs","instances":[{"attributes":{"id":"logs","arn":"arn:aws:s3:::logs"}}]},
	 {"mode":"managed","type":"aws_sqs_queue","name":"jobs","module":"module.q","instances":[{"attributes":{"id":"https://sqs.eu-north-1.amazonaws.com/123456789012/jobs"}}]},
	 {"mode":"data","type":"aws_vpc","name":"v","instances":[{"attributes":{"id":"vpc-1"}}]}]}`
	path := filepath.Join(t.TempDir(), "terraform.tfstate")
	_ = os.WriteFile(path, []byte(state), 0o600)
	n, err := AnnotateFromTerraformState(inv, []string{path})
	if err != nil || n != 2 {
		t.Fatalf("annotated %d: %v", n, err)
	}
	AnnotateFromTags(inv)
	AnnotateFromStacks(inv, []StackResource{{Stack: "net", PhysicalID: "vpc-1", Type: "AWS::EC2::VPC"}})
	want := []string{ManagedTerraform, ManagedTerraform, ManagedCloudFormation, ManagedCloudFormation}
	for i, r := range inv.Resources {
		if r.ManagedBy != want[i] {
			t.Errorf("%s: managed_by %q want %q (%s)", r.ID, r.ManagedBy, want[i], r.ManagedByEvidence)
		}
	}
	if !strings.Contains(inv.Resources[1].ManagedByEvidence, "module.q.aws_sqs_queue.jobs") {
		t.Errorf("evidence: %s", inv.Resources[1].ManagedByEvidence)
	}
	_ = os.WriteFile(path, []byte(`{"version":3}`), 0o600)
	if _, err := AnnotateFromTerraformState(inv, []string{path}); err == nil {
		t.Error("state v3 must be rejected")
	}
}

func TestImportAWSConfig(t *testing.T) {
	data := []byte(`{"Results":[
	 "{\"resourceId\":\"db-1\",\"resourceType\":\"AWS::RDS::DBInstance\",\"resourceName\":\"orders\",\"awsRegion\":\"eu-north-1\",\"arn\":\"arn:aws:rds:eu-north-1:123456789012:db:orders\",\"accountId\":\"123456789012\",\"tags\":[{\"key\":\"team\",\"value\":\"orders\"}],\"configuration\":{\"engine\":\"postgres\",\"multiAZ\":true,\"masterUserPassword\":\"never\"},\"relationships\":[{\"resourceId\":\"vpc-1\",\"relationshipName\":\"Is contained in Vpc\"}]}",
	 "{\"resourceId\":\"vpc-1\",\"resourceType\":\"AWS::EC2::VPC\",\"awsRegion\":\"eu-north-1\"}"]}`)
	inv, err := ImportAWSConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	idx := inv.Index()
	db := idx["arn:aws:rds:eu-north-1:123456789012:db:orders"]
	if db == nil || db.Completeness != CompletenessPartial || db.Attr("engine") != "postgres" || db.Attr("multi_az") != true || db.Tags["team"] != "orders" {
		t.Fatalf("db: %+v", db)
	}
	if _, leaked := db.Attributes["master_user_password"]; leaked {
		t.Fatal("sensitive configuration key kept")
	}
	if idx["vpc-1"].Completeness != CompletenessIdentifierOnly || len(inv.Relationships) != 1 || inv.Relationships[0].To != "vpc-1" {
		t.Fatalf("vpc/relationships: %+v %+v", idx["vpc-1"], inv.Relationships)
	}
	if len(inv.Gaps) != 1 || inv.Gaps[0].Reason != GapNotEnabled || inv.Metadata.Collector != CollectorAWSConfig {
		t.Fatalf("gaps/collector: %+v", inv.Gaps)
	}
	if _, err := ImportAWSConfig([]byte(`{"nope":1}`)); err == nil {
		t.Error("input without Results must be rejected")
	}
}

func TestSampleInventoryLoads(t *testing.T) {
	inv, err := Load(filepath.Join("..", "..", "examples", "migration", "sample-inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(inv.Resources); n < 40 || n > 70 || len(inv.Gaps) < 2 {
		t.Fatalf("sample: %d resources, %d gaps", n, len(inv.Gaps))
	}
	ids := inv.Index()
	for _, r := range inv.Relationships {
		if ids[r.From] == nil {
			t.Errorf("relationship from unknown resource %s", r.From)
		}
	}
}
