package awsinv

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/smithy-go/middleware"
)

// readPrefixes are the only operation-name prefixes a read-only collector may use.
var readPrefixes = []string{"Describe", "List", "Get", "Search", "Select", "Lookup", "Head"}

// deniedExact are read-named operations that return secret values, credentials, data or
// download URLs. They are refused even if someone adds them to the registry.
var deniedExact = map[string]bool{
	"GetFunction":                        true, // returns a pre-signed code download URL; use ListFunctions (env names only)
	"GetFunctionConfiguration":           true, // returns environment variable VALUES
	"GetLayerVersion":                    true, // returns a code download URL
	"GetLayerVersionByArn":               true,
	"GetDownloadUrlForLayer":             true, // ECR image layer download
	"GetAuthorizationToken":              true, // ECR registry credentials
	"GetCredentialsForIdentity":          true,
	"GetOpenIdToken":                     true,
	"GetOpenIdTokenForDeveloperIdentity": true,
	"GetSessionToken":                    true,
	"GetFederationToken":                 true,
	"GetRandomPassword":                  true,
	"GetPasswordData":                    true,
	"GetLaunchTemplateData":              true, // includes user data
	"GetItem":                            true,
	"GetRecords":                         true,
	"GetShardIterator":                   true,
	"GetSSHPublicKey":                    true,
	"GetQueryResults":                    true,
	// F-07 (defence in depth: only the registry stopped these before).
	"GetSnapshotBlock":               true, // EBS direct API: snapshot block DATA
	"GetFile":                        true, // CodeCommit file content
	"GetBlob":                        true, // CodeCommit blob content
	"GetFolder":                      true, // CodeCommit tree content
	"DescribeUserPoolClient":         true, // Cognito: returns the ClientSecret
	"GetConsoleOutput":               true, // EC2 console output (also covered by the GetConsole prefix)
	"GetConsoleScreenshot":           true,
	"Decrypt":                        true, // KMS plaintext
	"BatchGetSecretValue":            true, // also covered by the BatchGet prefix
	"GetParameterHistory":            true, // SSM values (also covered by the GetParameter prefix)
	"SelectObjectContent":            true, // S3 Select object content
	"GetObjectTorrent":               true, // S3 object data
	"GetJobOutput":                   true, // Glacier archive data
	"GetCommandInvocation":           true, // SSM Run Command output
	"DescribeInstanceAttribute":      true, // can return EC2 user data
	"DescribeLaunchTemplateVersions": true, // returns launch-template user data
}

// deniedPrefixes cover families of data or secret reads.
var deniedPrefixes = []string{
	"GetSecret",    // GetSecretValue
	"GetParameter", // GetParameter, GetParameters, GetParametersByPath, GetParameterHistory, GetParametersForImport
	"GetObject",    // S3 object data and object metadata
	"HeadObject",
	"SelectObject", // S3 Select reads object content
	"GetLogEvents",
	"GetLogRecord",
	"GetConsole", // console output / screenshots
	"GetTemplate",
	"GetMedia",
	"GetCredential", // GetCredentials*, GetCredentialReport
	"BatchGet",
	"GenerateDataKey", // KMS data keys (GenerateDataKey, ...WithoutPlaintext, ...Pair)
	"GetConnection",   // Glue connections carry passwords
	"GetApiKey",       // API Gateway v1 key values
}

// IsReadOnlyOperationName applies the naming rule: a read prefix and not denylisted.
func IsReadOnlyOperationName(op string) bool {
	if deniedExact[op] {
		return false
	}
	for _, p := range deniedPrefixes {
		if strings.HasPrefix(op, p) {
			return false
		}
	}
	for _, p := range readPrefixes {
		if strings.HasPrefix(op, p) && len(op) > len(p) && op[len(p)] >= 'A' && op[len(p)] <= 'Z' {
			return true
		}
	}
	return false
}

// GuardError is returned when the guard refuses an operation; nothing was sent.
type GuardError struct {
	ServiceID string
	Operation string
	Reason    string
}

func (e *GuardError) Error() string {
	return fmt.Sprintf("read-only guard refused %s:%s before sending: %s", e.ServiceID, e.Operation, e.Reason)
}

// Allowed reports whether the guard lets an operation through, and why not.
func Allowed(serviceID, op string) (bool, string) {
	if !IsReadOnlyOperationName(op) {
		return false, "operation is not a non-secret read (Describe/List/Get/Search/Select/Lookup/Head, denylist applied)"
	}
	if !allowed[opKey{serviceID, op}] {
		return false, "operation is not in the Hermes discovery registry"
	}
	return true, ""
}

// GuardMiddlewareID is the middleware id in the initialize step.
const GuardMiddlewareID = "HermesReadOnlyGuard"

// Recorder optionally records every operation that passed the guard (used by tests and
// the demo to prove which calls were made).
type Recorder struct {
	mu  sync.Mutex
	ops map[string]int
}

// NewRecorder returns an empty recorder.
func NewRecorder() *Recorder { return &Recorder{ops: map[string]int{}} }

func (r *Recorder) record(serviceID, op string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.ops[serviceID+":"+op]++
	r.mu.Unlock()
}

// Operations returns "ServiceID:Operation" -> call count.
func (r *Recorder) Operations() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.ops))
	for k, v := range r.ops {
		out[k] = v
	}
	return out
}

// GuardAPIOption returns an aws.Config APIOptions entry that installs the read-only guard
// as the very first initialize-step middleware of every operation, so a refused operation
// fails before validation, serialization, signing or any network I/O.
func GuardAPIOption(rec *Recorder) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Initialize.Add(middleware.InitializeMiddlewareFunc(GuardMiddlewareID,
			func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (middleware.InitializeOutput, middleware.Metadata, error) {
				svc := middleware.GetServiceID(ctx)
				op := middleware.GetOperationName(ctx)
				if op == "" {
					op = stack.ID()
				}
				if ok, why := Allowed(svc, op); !ok {
					return middleware.InitializeOutput{}, middleware.Metadata{}, &GuardError{ServiceID: svc, Operation: op, Reason: why}
				}
				rec.record(svc, op)
				return next.HandleInitialize(ctx, in)
			}), middleware.Before)
	}
}
