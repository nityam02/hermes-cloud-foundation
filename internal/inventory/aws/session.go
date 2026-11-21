package awsinv

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go/middleware"
)

// Discovery session constants.
const (
	SessionName     = "hermes-discovery"
	SessionDuration = time.Hour
)

// SessionOptions configures credentials for discovery.
type SessionOptions struct {
	Profile     string // shared config profile ("" = default chain)
	Region      string // home region for STS and global services
	RoleARN     string // optional cross-account read-only role
	ExternalID  string // ExternalId condition value for the role
	EndpointURL string // custom endpoint for every client (moto/LocalStack-style testing)
	MaxAttempts int    // SDK retry attempts (default 5)
	Recorder    *Recorder
}

// NewSession returns an aws.Config whose every client carries the read-only guard.
//
// Credentials come from the default chain or the named profile. With RoleARN, Hermes
// assumes the role through stscreds (session name "hermes-discovery", 1 hour, ExternalId
// when given). The AssumeRole call is made by the credential provider's own STS client,
// which is the only client without the guard and can only call AssumeRole.
func NewSession(ctx context.Context, o SessionOptions) (aws.Config, error) {
	if o.Region == "" {
		o.Region = "us-east-1"
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 5
	}
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(o.Region),
		config.WithRetryMaxAttempts(o.MaxAttempts),
	}
	if o.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(o.Profile))
	}
	if o.EndpointURL != "" {
		opts = append(opts, config.WithBaseEndpoint(o.EndpointURL))
	}
	base, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("aws: load configuration: %w", err)
	}
	if o.RoleARN != "" {
		stsClient := sts.NewFromConfig(base)
		provider := stscreds.NewAssumeRoleProvider(stsClient, o.RoleARN, func(ao *stscreds.AssumeRoleOptions) {
			ao.RoleSessionName = SessionName
			ao.Duration = SessionDuration
			if o.ExternalID != "" {
				ao.ExternalID = aws.String(o.ExternalID)
			}
		})
		base.Credentials = aws.NewCredentialsCache(provider)
	}
	guarded := base.Copy()
	guarded.APIOptions = append(append([]func(*middleware.Stack) error(nil), base.APIOptions...), GuardAPIOption(o.Recorder))
	return guarded, nil
}
