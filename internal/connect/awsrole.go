package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"

	awsinv "github.com/nityam02/hermes-cloud-foundation/internal/inventory/aws"
)

// The read-only discovery role is created as one CloudFormation stack with a fixed name, using the
// operator's own sign-in, only when the operator asks for it on the page. This is the one place the
// UI writes to AWS. It only ever touches this stack: discovery itself still runs through the
// read-only guard, as the role.

// RoleStackName is the stack that holds the discovery role.
const RoleStackName = "hermes-discovery-role"

// RoleStack is the state of the role stack.
type RoleStack struct {
	Status           string `json:"status"`
	RoleARN          string `json:"role_arn,omitempty"`
	ExternalID       string `json:"external_id,omitempty"`
	TrustedPrincipal string `json:"trusted_principal,omitempty"`
	Reason           string `json:"reason,omitempty"` // why the last change failed, when it did
}

// StackAPI is the CloudFormation calls on the role stack, replaceable in tests.
type StackAPI interface {
	// Describe returns nil when the stack does not exist.
	Describe(ctx context.Context) (*RoleStack, error)
	Create(ctx context.Context, body string, params map[string]string) error
	// Update returns false when the stack already matches. An empty parameter value keeps the
	// previous value.
	Update(ctx context.Context, body string, params map[string]string) (bool, error)
	Delete(ctx context.Context) error
	// FailureReason is the first failure in the stack's recent events.
	FailureReason(ctx context.Context) string
}

// RoleRequest asks for the role to trust one principal with one external ID.
type RoleRequest struct {
	Profile    string `json:"profile"`
	Region     string `json:"region"`
	Principal  string `json:"principal"`   // the operator's own ARN (never the root user)
	ExternalID string `json:"external_id"` // used when the stack is new; an existing stack keeps its own
}

func stackComplete(s string) bool {
	switch s {
	case "CREATE_COMPLETE", "UPDATE_COMPLETE", "UPDATE_ROLLBACK_COMPLETE", "IMPORT_COMPLETE", "IMPORT_ROLLBACK_COMPLETE":
		return true
	}
	return false
}

func stackBusy(s string) bool { return strings.HasSuffix(s, "_IN_PROGRESS") }

// EnsureRole creates the role stack, or updates it to the current template and principal, and waits
// until it is ready. It returns the role ARN and the external ID the role requires.
func EnsureRole(ctx context.Context, api StackAPI, req RoleRequest, p Progress, poll time.Duration) (RoleStack, error) {
	if !strings.HasPrefix(req.Principal, "arn:aws") || IsRootARN(req.Principal) {
		return RoleStack{}, errors.New("the role must trust an IAM user, role or SSO identity, not the root user")
	}
	p.Step("find", "running", "Looking for the Hermes role stack.")
	cur, err := api.Describe(ctx)
	if err != nil {
		return RoleStack{}, explainStackErr(err)
	}
	if cur != nil && stackBusy(cur.Status) {
		p.Step("find", "running", "The stack is busy ("+cur.Status+"). Waiting for it to finish.")
		if cur, err = waitStack(ctx, api, p, "find", poll); err != nil {
			return RoleStack{}, err
		}
	}
	body := string(RoleTemplate())
	switch {
	case cur == nil || cur.Status == "DELETE_COMPLETE":
		p.Step("find", "done", "No role yet. Hermes will create it.")
	case cur.Status == "ROLLBACK_COMPLETE" || cur.Status == "ROLLBACK_FAILED" || cur.Status == "CREATE_FAILED":
		p.Step("find", "done", "A failed earlier attempt is in the way ("+cur.Status+"). Hermes will remove it first.")
		if err := removeStack(ctx, api, p, poll); err != nil {
			return RoleStack{}, err
		}
		cur = nil
	case stackComplete(cur.Status):
		p.Step("find", "done", "Found the role from an earlier setup.")
		p.Step("update", "running", "Bringing the role up to date with the latest read-only permissions.")
		ext := "" // keep the stack's own external ID
		if cur.ExternalID == "" {
			// An older template hid it, so nobody can use the role through Hermes: give it a new one.
			ext = req.ExternalID
			if len(ext) < externalIDMin {
				ext = NewExternalID()
			}
			p.Step("update", "running", "The role's external ID is hidden by an older template. Setting a new one and the latest read-only permissions.")
		}
		changed, err := api.Update(ctx, body, map[string]string{"ExternalId": ext, "TrustedPrincipalArn": req.Principal, "RoleName": ""})
		if err != nil {
			return RoleStack{}, explainStackErr(err)
		}
		if changed {
			if cur, err = waitStack(ctx, api, p, "update", poll); err != nil {
				return RoleStack{}, err
			}
			if cur == nil {
				return RoleStack{}, errors.New("the stack disappeared while it was being updated")
			}
			if !stackComplete(cur.Status) || cur.Status == "UPDATE_ROLLBACK_COMPLETE" {
				return RoleStack{}, fmt.Errorf("AWS could not update the role (%s): %s", cur.Status, firstNonEmpty(api.FailureReason(ctx), "see the stack events in the CloudFormation console"))
			}
			p.Step("update", "done", "Updated.")
		} else {
			p.Step("update", "done", "Already up to date.")
		}
		return ready(cur)
	default:
		return RoleStack{}, fmt.Errorf("the stack %s is %s, which Hermes will not change. Fix or delete it in the CloudFormation console, then try again", RoleStackName, cur.Status)
	}

	ext := req.ExternalID
	if len(ext) < externalIDMin {
		ext = NewExternalID()
	}
	p.Step("create", "running", "Creating the read-only role. This usually takes under a minute.")
	if err := api.Create(ctx, body, map[string]string{"ExternalId": ext, "TrustedPrincipalArn": req.Principal}); err != nil {
		return RoleStack{}, explainStackErr(err)
	}
	cur, err = waitStack(ctx, api, p, "create", poll)
	if err != nil {
		return RoleStack{}, err
	}
	if cur == nil || cur.Status != "CREATE_COMPLETE" {
		reason := firstNonEmpty(api.FailureReason(ctx), "see the stack events in the CloudFormation console")
		status := "deleted"
		if cur != nil {
			status = cur.Status
		}
		return RoleStack{}, fmt.Errorf("AWS could not create the role (%s): %s", status, reason)
	}
	p.Step("create", "done", "Created.")
	return ready(cur)
}

func ready(s *RoleStack) (RoleStack, error) {
	if s == nil || s.RoleARN == "" {
		return RoleStack{}, errors.New("the stack has no RoleArn output; it was not made from the Hermes template")
	}
	return *s, nil
}

// RemoveRole deletes the role stack and waits until it is gone.
func RemoveRole(ctx context.Context, api StackAPI, p Progress, poll time.Duration) error {
	cur, err := api.Describe(ctx)
	if err != nil {
		return explainStackErr(err)
	}
	if cur == nil || cur.Status == "DELETE_COMPLETE" {
		p.Step("delete", "done", "There is no Hermes role in this account.")
		return nil
	}
	return removeStack(ctx, api, p, poll)
}

func removeStack(ctx context.Context, api StackAPI, p Progress, poll time.Duration) error {
	p.Step("delete", "running", "Deleting the stack "+RoleStackName+".")
	if err := api.Delete(ctx); err != nil {
		return explainStackErr(err)
	}
	cur, err := waitStack(ctx, api, p, "delete", poll)
	if err != nil {
		return err
	}
	if cur != nil && cur.Status != "DELETE_COMPLETE" {
		return fmt.Errorf("AWS could not delete the stack (%s): %s", cur.Status, firstNonEmpty(api.FailureReason(ctx), "see the stack events"))
	}
	p.Step("delete", "done", "Deleted.")
	return nil
}

func waitStack(ctx context.Context, api StackAPI, p Progress, step string, poll time.Duration) (*RoleStack, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, errors.New("stopped waiting for CloudFormation; the stack may still be changing")
		case <-time.After(poll):
		}
		cur, err := api.Describe(ctx)
		if err != nil {
			return nil, explainStackErr(err)
		}
		if cur == nil || !stackBusy(cur.Status) {
			return cur, nil
		}
		p.Step(step, "running", "CloudFormation: "+strings.ToLower(strings.ReplaceAll(cur.Status, "_", " "))+".")
	}
}

func explainStackErr(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "AccessDenied") || strings.Contains(msg, "not authorized"):
		return ErrSetupDenied
	case strings.Contains(msg, "InsufficientCapabilities"):
		return errors.New("CloudFormation needs the IAM capability to create the role")
	case strings.Contains(msg, "ExpiredToken") || strings.Contains(msg, "failed to refresh cached credentials") || strings.Contains(msg, "login token") || strings.Contains(msg, "login session"):
		return errors.New("your AWS sign-in has expired. Sign in again, then try again")
	}
	return fmt.Errorf("CloudFormation: %s", truncate(msg, 300))
}

// ErrSetupDenied means the operator's sign-in may not look up or change the role stack.
var ErrSetupDenied = errors.New("your sign-in is not allowed to manage the Hermes role stack in this account. An administrator can attach the setup policy below to your sign-in, or you can create the role yourself")

// SetupPolicy is the least privilege an operator needs to create, update and remove the role stack
// from the page: CloudFormation on that one stack, and IAM on that one role.
func SetupPolicy(account string) []byte {
	if account == "" {
		account = "*"
	}
	doc := map[string]any{
		"Version": "2012-10-17",
		"Statement": []any{
			map[string]any{"Sid": "HermesRoleStack", "Effect": "Allow",
				"Action": []string{"cloudformation:CreateStack", "cloudformation:UpdateStack", "cloudformation:DeleteStack",
					"cloudformation:DescribeStacks", "cloudformation:DescribeStackEvents"},
				"Resource": "arn:aws:cloudformation:*:" + account + ":stack/" + RoleStackName + "/*"},
			map[string]any{"Sid": "HermesRole", "Effect": "Allow",
				"Action": []string{"iam:CreateRole", "iam:DeleteRole", "iam:GetRole", "iam:UpdateRole", "iam:UpdateRoleDescription",
					"iam:UpdateAssumeRolePolicy", "iam:PutRolePolicy", "iam:DeleteRolePolicy", "iam:GetRolePolicy",
					"iam:ListRolePolicies", "iam:ListAttachedRolePolicies", "iam:TagRole", "iam:UntagRole", "iam:ListRoleTags"},
				"Resource": "arn:aws:iam::" + account + ":role/HermesDiscoveryReadOnly"},
		},
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return b
}

// cfnStack is the real StackAPI: CloudFormation with the operator's own sign-in. It is not the
// guarded discovery client, and it only ever names RoleStackName.
type cfnStack struct{ c *cloudformation.Client }

// NewRoleStackAPI returns the CloudFormation calls for the role stack in one region.
func NewRoleStackAPI(ctx context.Context, profile, region string) (StackAPI, error) {
	if region == "" {
		region = "us-east-1"
	}
	opts := []func(*config.LoadOptions) error{config.WithRegion(region)}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &cfnStack{c: cloudformation.NewFromConfig(cfg)}, nil
}

func (s *cfnStack) Describe(ctx context.Context) (*RoleStack, error) {
	out, err := s.c.DescribeStacks(ctx, &cloudformation.DescribeStacksInput{StackName: aws.String(RoleStackName)})
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return nil, nil
		}
		return nil, err
	}
	if len(out.Stacks) == 0 {
		return nil, nil
	}
	st := out.Stacks[0]
	r := &RoleStack{Status: string(st.StackStatus), Reason: aws.ToString(st.StackStatusReason)}
	for _, o := range st.Outputs {
		if aws.ToString(o.OutputKey) == "RoleArn" {
			r.RoleARN = aws.ToString(o.OutputValue)
		}
	}
	for _, p := range st.Parameters {
		switch aws.ToString(p.ParameterKey) {
		case "ExternalId":
			// Stacks made from older templates hid the external ID (NoEcho), so AWS returns "****".
			if v := aws.ToString(p.ParameterValue); v != "" && strings.Trim(v, "*") != "" {
				r.ExternalID = v
			}
		case "TrustedPrincipalArn":
			r.TrustedPrincipal = aws.ToString(p.ParameterValue)
		}
	}
	return r, nil
}

func cfnParams(params map[string]string) []cfntypes.Parameter {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []cfntypes.Parameter
	for _, k := range keys {
		if params[k] == "" {
			out = append(out, cfntypes.Parameter{ParameterKey: aws.String(k), UsePreviousValue: aws.Bool(true)})
		} else {
			out = append(out, cfntypes.Parameter{ParameterKey: aws.String(k), ParameterValue: aws.String(params[k])})
		}
	}
	return out
}

var stackTags = []cfntypes.Tag{{Key: aws.String("hermes-managed-by"), Value: aws.String("hermes")}}

func (s *cfnStack) Create(ctx context.Context, body string, params map[string]string) error {
	_, err := s.c.CreateStack(ctx, &cloudformation.CreateStackInput{
		StackName: aws.String(RoleStackName), TemplateBody: aws.String(body), Parameters: cfnParams(params),
		Capabilities: []cfntypes.Capability{cfntypes.CapabilityCapabilityNamedIam}, Tags: stackTags,
	})
	return err
}

func (s *cfnStack) Update(ctx context.Context, body string, params map[string]string) (bool, error) {
	_, err := s.c.UpdateStack(ctx, &cloudformation.UpdateStackInput{
		StackName: aws.String(RoleStackName), TemplateBody: aws.String(body), Parameters: cfnParams(params),
		Capabilities: []cfntypes.Capability{cfntypes.CapabilityCapabilityNamedIam}, Tags: stackTags,
	})
	if err != nil && strings.Contains(err.Error(), "No updates are to be performed") {
		return false, nil
	}
	return err == nil, err
}

func (s *cfnStack) Delete(ctx context.Context) error {
	_, err := s.c.DeleteStack(ctx, &cloudformation.DeleteStackInput{StackName: aws.String(RoleStackName)})
	return err
}

func (s *cfnStack) FailureReason(ctx context.Context) string {
	out, err := s.c.DescribeStackEvents(ctx, &cloudformation.DescribeStackEventsInput{StackName: aws.String(RoleStackName)})
	if err != nil {
		return ""
	}
	// Events are newest first; the earliest failure of the latest attempt is the cause.
	reason := ""
	for _, e := range out.StackEvents {
		st := string(e.ResourceStatus)
		if strings.HasSuffix(st, "_FAILED") && aws.ToString(e.ResourceStatusReason) != "" && !strings.Contains(aws.ToString(e.ResourceStatusReason), "cancelled") {
			reason = aws.ToString(e.ResourceStatusReason)
		}
		if aws.ToString(e.LogicalResourceId) == RoleStackName && (st == "CREATE_IN_PROGRESS" || st == "UPDATE_IN_PROGRESS") {
			break
		}
	}
	return truncate(reason, 300)
}

// WaitUsable waits until AWS lets the operator assume a new or changed role: IAM changes take a few
// seconds to reach STS everywhere.
func WaitUsable(ctx context.Context, req AWSRequest, p Progress, assume func(context.Context, AWSRequest) error, tries int, delay time.Duration) error {
	p.Step("ready", "running", "Waiting for AWS to make the role usable.")
	var err error
	for i := 0; i < tries; i++ {
		if err = assume(ctx, req); err == nil {
			p.Step("ready", "done", "You can assume the role.")
			return nil
		}
		if !strings.Contains(err.Error(), "AccessDenied") && !strings.Contains(err.Error(), "not authorized") {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	title, fix := classifyAWS(err, req, "")
	return fmt.Errorf("%s %s", title, fix)
}

// AssumeRole gets credentials for the discovery role once, to prove it can be used.
func AssumeRole(ctx context.Context, req AWSRequest) error {
	cfg, err := awsinv.NewSession(ctx, awsinv.SessionOptions{Profile: req.Profile, Region: firstNonEmpty(req.Region, "us-east-1"), RoleARN: req.RoleARN, ExternalID: req.ExternalID})
	if err != nil {
		return err
	}
	_, err = cfg.Credentials.Retrieve(ctx)
	return err
}

// FindRole reports the role stack, or nil when there is none.
func FindRole(ctx context.Context, profile, region string) (*RoleStack, error) {
	api, err := NewRoleStackAPI(ctx, profile, region)
	if err != nil {
		return nil, err
	}
	s, err := api.Describe(ctx)
	if err != nil {
		return nil, explainStackErr(err)
	}
	return s, nil
}
