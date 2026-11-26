package awsinv

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go/middleware"
)

func TestRegistryOperationsAreReadOnly(t *testing.T) {
	ops := Registry()
	if len(ops) < 90 {
		t.Fatalf("registry unexpectedly small: %d operations", len(ops))
	}
	for _, op := range ops {
		if ok, why := Allowed(op.ServiceID, op.Name); !ok {
			t.Errorf("%s:%s is in the registry but refused: %s", op.ServiceID, op.Name, why)
		}
		if op.IAMAction != "" && (strings.Contains(op.IAMAction, "*") || !strings.Contains(op.IAMAction, ":")) {
			t.Errorf("%s: invalid IAM action %q", op.Name, op.IAMAction)
		}
	}
}

func TestGuardRejectsWritesAndSecretReads(t *testing.T) {
	refused := map[string][]string{
		s3.ServiceID:      {"DeleteBucket", "PutObject", "GetObject", "GetObjectAcl", "HeadObject", "SelectObjectContent", "PutBucketPolicy", "CreateBucket"},
		"EC2":             {"CreateVpc", "TerminateInstances", "ModifyInstanceAttribute", "GetPasswordData", "GetConsoleOutput", "GetLaunchTemplateData", "RunInstances"},
		"Secrets Manager": {"GetSecretValue", "BatchGetSecretValue", "PutSecretValue", "GetRandomPassword"},
		"SSM":             {"GetParameter", "GetParameters", "GetParametersByPath", "GetParameterHistory", "PutParameter"},
		"Lambda":          {"GetFunction", "GetLayerVersion", "Invoke", "UpdateFunctionCode"},
		"ECR":             {"GetDownloadUrlForLayer", "GetAuthorizationToken", "BatchGetImage"},
		"SQS":             {"ReceiveMessage", "SendMessage", "PurgeQueue", "DeleteQueue"},
		"STS":             {"AssumeRole", "GetSessionToken", "GetFederationToken"},
		"KMS":             {"Decrypt", "GenerateDataKey", "ScheduleKeyDeletion"},
		"Kinesis":         {"GetRecords", "GetShardIterator"},
		"DynamoDB":        {"GetItem", "Scan", "Query", "DeleteTable"},
		"RDS":             {"ModifyDBInstance", "DeleteDBInstance", "RebootDBInstance"},
		"CloudFormation":  {"GetTemplate", "DeleteStack"},
		"CloudWatch Logs": {"GetLogEvents", "FilterLogEvents"},
	}
	for svc, ops := range refused {
		for _, op := range ops {
			if ok, _ := Allowed(svc, op); ok {
				t.Errorf("guard allowed %s:%s", svc, op)
			}
		}
	}
	// The naming rule alone (defence in depth if someone extends the registry).
	for _, op := range []string{"GetSecretValue", "GetObject", "GetFunction", "GetParametersByPath", "GetCredentialsForIdentity", "DeleteBucket"} {
		if IsReadOnlyOperationName(op) {
			t.Errorf("naming rule allowed %s", op)
		}
	}
	for _, op := range []string{"GetFunctionConcurrency", "DescribeParameters", "ListSecrets", "GetBucketPolicy", "GetQueueUrl", "GetApis", "DescribeInstances"} {
		if !IsReadOnlyOperationName(op) {
			t.Errorf("naming rule refused harmless read %s", op)
		}
	}
	// Prefix must be followed by an upper-case letter: "Getaway" is not a read.
	if IsReadOnlyOperationName("Listen") || IsReadOnlyOperationName("Describe") {
		t.Error("bare or lower-case-continued prefixes must not pass")
	}
}

// F-07: data and secret reads are refused by the naming rule itself, so they stay refused even
// if someone adds them to the registry (simulated here by inserting them into the allowlist).
func TestGuardDenylistHoldsEvenIfRegistered(t *testing.T) {
	ops := []string{"GetSnapshotBlock", "GetFile", "GetBlob", "GetFolder", "DescribeUserPoolClient", "GetPasswordData",
		"GetConsoleOutput", "GetConsoleScreenshot", "Decrypt", "GenerateDataKey", "GenerateDataKeyWithoutPlaintext",
		"GenerateDataKeyPair", "GetSessionToken", "GetFederationToken", "BatchGetSecretValue", "GetParameterHistory",
		"SelectObjectContent", "GetObjectTorrent", "GetRandomPassword",
		// also from the QA review
		"GetJobOutput", "GetCommandInvocation", "GetFunctionConfiguration", "DescribeInstanceAttribute",
		"DescribeLaunchTemplateVersions", "GetConnection", "GetConnections", "GetApiKey", "GetApiKeys", "GetCredentialReport"}
	const svc = "TestService"
	for _, op := range ops {
		k := opKey{svc, op}
		allowed[k] = true
		ok, reason := Allowed(svc, op)
		delete(allowed, k)
		if ok || !strings.Contains(reason, "not a non-secret read") {
			t.Errorf("%s must be refused by the naming rule even when registered (ok=%v, %q)", op, ok, reason)
		}
	}
}

// TestCollectorInterfacesAreInRegistry proves that every operation a collector can call is
// allowlisted: collectors only hold the interfaces in Clients, so their method sets are the
// complete set of callable operations.
func TestCollectorInterfacesAreInRegistry(t *testing.T) {
	sdkIDs := map[string]string{
		"STS": "STS", "EC2": "EC2", "ELBv2": "Elastic Load Balancing v2", "RDS": "RDS", "S3": "S3", "CloudWatch": "CloudWatch",
		"ECS": "ECS", "EKS": "EKS", "Lambda": "Lambda", "ECR": "ECR", "SQS": "SQS", "SNS": "SNS", "DynamoDB": "DynamoDB",
		"ElastiCache": "ElastiCache", "Kinesis": "Kinesis", "Route53": "Route 53", "ACM": "ACM", "IAM": "IAM",
		"SecretsManager": "Secrets Manager", "SSM": "SSM", "KMS": "KMS", "Logs": "CloudWatch Logs", "CloudFront": "CloudFront",
		"APIGatewayV2": "ApiGatewayV2", "SFN": "SFN", "EventBridge": "EventBridge", "CloudFormation": "CloudFormation",
		"CloudControl": "CloudControl",
	}
	used := map[string]bool{}
	ct := reflect.TypeOf(Clients{})
	for i := 0; i < ct.NumField(); i++ {
		f := ct.Field(i)
		sdkID, ok := sdkIDs[f.Name]
		if !ok {
			t.Fatalf("Clients.%s has no ServiceID mapping in this test", f.Name)
		}
		for m := 0; m < f.Type.NumMethod(); m++ {
			op := f.Type.Method(m).Name
			used[sdkID+":"+op] = true
			if ok, why := Allowed(sdkID, op); !ok {
				t.Errorf("Clients.%s.%s is callable by collectors but refused by the guard: %s", f.Name, op, why)
			}
		}
	}
	// And the registry lists nothing collectors cannot call (least privilege).
	for _, op := range Registry() {
		if !used[op.ServiceID+":"+op.Name] {
			t.Errorf("registry grants %s:%s but no collector interface uses it", op.ServiceID, op.Name)
		}
	}
}

// TestGuardMiddlewareBlocksBeforeSending uses a real S3 client against a local server:
// a refused operation must fail without any HTTP request.
func TestGuardMiddlewareBlocksBeforeSending(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><ListAllMyBucketsResult><Buckets></Buckets></ListAllMyBucketsResult>`)
	}))
	defer srv.Close()
	rec := NewRecorder()
	cfg := aws.Config{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider("AKIDEXAMPLEFAKE", "demo-not-a-secret", ""),
		BaseEndpoint: aws.String(srv.URL),
		APIOptions:   []func(*middleware.Stack) error{GuardAPIOption(rec)},
	}
	c := s3.NewFromConfig(cfg, func(o *s3.Options) { o.UsePathStyle = true })
	ctx := context.Background()

	_, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), Body: bytes.NewReader([]byte("x"))})
	var ge *GuardError
	if !errors.As(err, &ge) || ge.Operation != "PutObject" {
		t.Fatalf("PutObject: want GuardError, got %v", err)
	}
	if _, err := c.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String("b")}); !errors.As(err, &ge) {
		t.Fatalf("DeleteBucket: want GuardError, got %v", err)
	}
	if _, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("b"), Key: aws.String("k")}); !errors.As(err, &ge) {
		t.Fatalf("GetObject: want GuardError, got %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("refused operations reached the server %d time(s)", n)
	}
	if _, err := c.ListBuckets(ctx, &s3.ListBucketsInput{}); err != nil {
		t.Fatalf("ListBuckets should pass the guard: %v", err)
	}
	if atomic.LoadInt32(&hits) != 1 || rec.Operations()["S3:ListBuckets"] != 1 {
		t.Fatalf("allowed call not recorded/sent: hits=%d ops=%v", hits, rec.Operations())
	}
}

// TestSessionAssumesRoleWithExternalID runs the full credential flow against a local fake
// STS: AssumeRole (session name, 1h, ExternalId) and then a guarded call signed with the
// assumed credentials.
func TestSessionAssumesRoleWithExternalID(t *testing.T) {
	isolateAWSEnv(t)
	var assumeForm url.Values
	var callerAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "text/xml")
		switch r.Form.Get("Action") {
		case "AssumeRole":
			assumeForm = r.Form
			_, _ = io.WriteString(w, `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult><Credentials><AccessKeyId>ASIAEXAMPLEASSUMED</AccessKeyId><SecretAccessKey>demo-not-a-secret</SecretAccessKey><SessionToken>demo-not-a-secret</SessionToken><Expiration>2099-01-01T00:00:00Z</Expiration></Credentials><AssumedRoleUser><Arn>arn:aws:sts::123456789012:assumed-role/HermesDiscoveryReadOnly/hermes-discovery</Arn><AssumedRoleId>AROAEXAMPLE:hermes-discovery</AssumedRoleId></AssumedRoleUser></AssumeRoleResult><ResponseMetadata><RequestId>1</RequestId></ResponseMetadata></AssumeRoleResponse>`)
		case "GetCallerIdentity":
			callerAuth = r.Header.Get("Authorization")
			_, _ = io.WriteString(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Arn>arn:aws:sts::123456789012:assumed-role/HermesDiscoveryReadOnly/hermes-discovery</Arn><UserId>AROAEXAMPLE:hermes-discovery</UserId><Account>123456789012</Account></GetCallerIdentityResult><ResponseMetadata><RequestId>2</RequestId></ResponseMetadata></GetCallerIdentityResponse>`)
		default:
			w.WriteHeader(400)
		}
	}))
	defer srv.Close()
	cfg, err := NewSession(context.Background(), SessionOptions{
		Region: "us-east-1", EndpointURL: srv.URL,
		RoleARN: "arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly", ExternalID: "demo-external-id",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(context.Background(), &sts.GetCallerIdentityInput{})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(out.Account) != "123456789012" {
		t.Fatalf("account = %q", aws.ToString(out.Account))
	}
	if assumeForm.Get("ExternalId") != "demo-external-id" || assumeForm.Get("RoleSessionName") != SessionName || assumeForm.Get("DurationSeconds") != "3600" {
		t.Fatalf("AssumeRole parameters wrong: %v", assumeForm)
	}
	if !strings.Contains(callerAuth, "Credential=ASIAEXAMPLEASSUMED/") {
		t.Fatalf("guarded call was not signed with the assumed-role credentials: %q", callerAuth)
	}
	// The guarded config must refuse AssumeRole itself (only the credential provider may call it).
	_, err = sts.NewFromConfig(cfg).AssumeRole(context.Background(), &sts.AssumeRoleInput{RoleArn: aws.String("arn:aws:iam::123456789012:role/x"), RoleSessionName: aws.String("x")})
	var ge *GuardError
	if !errors.As(err, &ge) {
		t.Fatalf("guarded AssumeRole: want GuardError, got %v", err)
	}
}

func isolateAWSEnv(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"AWS_CONFIG_FILE": os.DevNull, "AWS_SHARED_CREDENTIALS_FILE": os.DevNull, "AWS_PROFILE": "",
		"AWS_EC2_METADATA_DISABLED": "true", "AWS_ACCESS_KEY_ID": "AKIDEXAMPLEFAKE", "AWS_SECRET_ACCESS_KEY": "demo-not-a-secret",
		"AWS_SESSION_TOKEN": "", "AWS_REGION": "us-east-1", "AWS_ENDPOINT_URL": "", "AWS_ROLE_ARN": "", "AWS_WEB_IDENTITY_TOKEN_FILE": "",
	} {
		t.Setenv(k, v)
	}
}

func TestDiscoveryPolicyFileMatchesRegistry(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "migration", "discovery", "aws-readonly-discovery-policy.json")
	want := DiscoveryPolicyJSON()
	if os.Getenv("HERMES_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (regenerate with HERMES_UPDATE_GOLDEN=1 go test ./internal/inventory/aws/ -run Policy)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is out of date with the operation registry; regenerate with HERMES_UPDATE_GOLDEN=1", path)
	}
	doc := DiscoveryPolicy()
	allow := map[string]bool{}
	for _, st := range doc.Statement {
		for _, a := range st.Action {
			if strings.Contains(a, "*") {
				t.Errorf("wildcard action %q", a)
			}
			if st.Effect == "Allow" {
				allow[a] = true
			}
		}
	}
	for _, d := range DeniedIAMActions {
		if allow[d] {
			t.Errorf("%s is both allowed and denied", d)
		}
	}
	for _, must := range []string{"secretsmanager:GetSecretValue", "s3:GetObject", "ssm:GetParameter", "lambda:GetFunction", "sqs:ReceiveMessage", "kms:Decrypt"} {
		found := false
		for _, d := range DeniedIAMActions {
			found = found || d == must
		}
		if !found {
			t.Errorf("deny statement misses %s", must)
		}
	}
}

func TestDiscoveryPolicyIncludesImpliedActionsAndExternalIDIsPrefillable(t *testing.T) {
	allow := map[string]bool{}
	for _, st := range DiscoveryPolicy().Statement {
		if st.Effect == "Allow" {
			for _, a := range st.Action {
				allow[a] = true
			}
		}
	}
	if !allow["cloudformation:ListStacks"] {
		t.Error("DescribeStacks without a stack name needs cloudformation:ListStacks (real-account finding)")
	}
	// Quick-create links ignore NoEcho parameters, and AWS does not treat the external ID as a secret.
	if strings.Contains(string(DiscoveryRoleTemplate()), "NoEcho") {
		t.Error("ExternalId must not be NoEcho: quick-create links could not prefill it")
	}
}

func TestDiscoveryRoleArtifactsMatchRegistry(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	disc := filepath.Join(filepath.Dir(file), "..", "..", "..", "migration", "discovery")
	golden := map[string][]byte{
		filepath.Join(disc, "cloudformation", "hermes-discovery-role.json"): DiscoveryRoleTemplate(),
		filepath.Join(disc, "terraform", "policy.json"):                     DiscoveryPolicyJSON(),
	}
	for path, want := range golden {
		if os.Getenv("HERMES_UPDATE_GOLDEN") == "1" {
			_ = os.MkdirAll(filepath.Dir(path), 0o750)
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is missing or out of date with the operation registry (regenerate with HERMES_UPDATE_GOLDEN=1): %v", path, err)
		}
	}
	tpl := string(DiscoveryRoleTemplate())
	for _, want := range []string{`"sts:ExternalId"`, `"MaxSessionDuration": 3600`, `"HermesDenySecretAndDataReads"`} {
		if !strings.Contains(tpl, want) {
			t.Errorf("CloudFormation template lacks %s", want)
		}
	}
}

func TestClassifyError(t *testing.T) {
	cases := map[string]string{
		"AccessDenied": "access-denied", "UnauthorizedOperation": "access-denied", "AuthorizationError": "access-denied",
		"OptInRequired": "opt-in-region-disabled", "ThrottlingException": "throttled", "RequestLimitExceeded": "throttled",
		"UnsupportedOperation": "unsupported", "InternalError": "error",
	}
	for code, want := range cases {
		if got, _ := ClassifyError(apiErr(code, "x")); got != want {
			t.Errorf("%s: got %s want %s", code, got, want)
		}
	}
	if got, _ := ClassifyError(apiErr("InternalFailure", "The ListX action has not been implemented")); got != "unsupported" {
		t.Errorf("moto not-implemented: got %s", got)
	}
	if got, _ := ClassifyError(&GuardError{ServiceID: "S3", Operation: "PutObject"}); got != "error" {
		t.Errorf("guard error: got %s", got)
	}
}
