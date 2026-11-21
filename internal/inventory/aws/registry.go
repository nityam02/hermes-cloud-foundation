// Package awsinv discovers AWS resources directly through the official AWS APIs (SDK for
// Go v2), read-only and agentless, without any Terraform/CloudFormation state.
//
// Safety model:
//   - Every API operation Hermes may call is listed in the operation registry below. The
//     read-only guard middleware rejects, before anything is sent, any operation that is
//     not in the registry, not named like a read (Describe/List/Get/...), or on the denylist
//     of reads that return secret values or data (GetSecretValue, GetObject, ...).
//   - The least-privilege IAM policy in migration/discovery/ is generated from the same
//     registry (tests assert they match), with an explicit Deny for data/secret reads.
package awsinv

import (
	"sort"

	"github.com/aws/aws-sdk-go-v2/service/acm"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Operation is one AWS API operation Hermes discovery may call.
type Operation struct {
	Service   string // Hermes service name (--services filter, gaps[].service)
	ServiceID string // SDK ServiceID, as seen by middleware
	Name      string // API operation name
	IAMAction string // IAM action required ("" when the API needs no permission)
}

type svcDef struct {
	name, sdkID, iamPrefix string
	ops                    []string
	actions                map[string]string // operation -> IAM action when it differs from prefix:op
}

// Hermes service names.
const (
	SvcSTS            = "sts"
	SvcEC2            = "ec2"
	SvcELBv2          = "elbv2"
	SvcRDS            = "rds"
	SvcS3             = "s3"
	SvcCloudWatch     = "cloudwatch"
	SvcECS            = "ecs"
	SvcEKS            = "eks"
	SvcLambda         = "lambda"
	SvcECR            = "ecr"
	SvcSQS            = "sqs"
	SvcSNS            = "sns"
	SvcDynamoDB       = "dynamodb"
	SvcElastiCache    = "elasticache"
	SvcKinesis        = "kinesis"
	SvcRoute53        = "route53"
	SvcACM            = "acm"
	SvcIAM            = "iam"
	SvcSecretsManager = "secretsmanager"
	SvcSSM            = "ssm"
	SvcKMS            = "kms"
	SvcLogs           = "logs"
	SvcCloudFront     = "cloudfront"
	SvcAPIGatewayV2   = "apigatewayv2"
	SvcSFN            = "sfn"
	SvcEvents         = "events"
	SvcCloudFormation = "cloudformation"
	SvcCloudControl   = "cloudcontrol"
)

var services = []svcDef{
	{name: SvcSTS, sdkID: sts.ServiceID, iamPrefix: "sts", ops: []string{"GetCallerIdentity"},
		actions: map[string]string{"GetCallerIdentity": ""}}, // needs no permission
	{name: SvcEC2, sdkID: ec2.ServiceID, iamPrefix: "ec2", ops: []string{
		"DescribeRegions", "DescribeVpcs", "DescribeSubnets", "DescribeSecurityGroups", "DescribeRouteTables",
		"DescribeInternetGateways", "DescribeNatGateways", "DescribeInstances", "DescribeVolumes",
		"DescribeTransitGateways", "DescribeTransitGatewayAttachments", "DescribeVpnConnections",
		"DescribeVpnGateways", "DescribeVpcEndpoints", "DescribeVpcPeeringConnections"}},
	{name: SvcELBv2, sdkID: elbv2.ServiceID, iamPrefix: "elasticloadbalancing", ops: []string{
		"DescribeLoadBalancers", "DescribeListeners", "DescribeTargetGroups", "DescribeTargetHealth", "DescribeTags"}},
	{name: SvcRDS, sdkID: rds.ServiceID, iamPrefix: "rds", ops: []string{
		"DescribeDBInstances", "DescribeDBClusters", "DescribeDBSubnetGroups"}},
	{name: SvcS3, sdkID: s3.ServiceID, iamPrefix: "s3", ops: []string{
		"ListBuckets", "GetBucketLocation", "GetBucketVersioning", "GetBucketEncryption", "GetPublicAccessBlock",
		"GetBucketOwnershipControls", "GetBucketLifecycleConfiguration", "GetBucketReplication",
		"GetBucketPolicy", "GetBucketPolicyStatus", "GetBucketTagging"},
		actions: map[string]string{
			"ListBuckets":                     "s3:ListAllMyBuckets",
			"GetBucketEncryption":             "s3:GetEncryptionConfiguration",
			"GetPublicAccessBlock":            "s3:GetBucketPublicAccessBlock",
			"GetBucketLifecycleConfiguration": "s3:GetLifecycleConfiguration",
			"GetBucketReplication":            "s3:GetReplicationConfiguration",
		}},
	{name: SvcCloudWatch, sdkID: cloudwatch.ServiceID, iamPrefix: "cloudwatch", ops: []string{"GetMetricData", "DescribeAlarms"}},
	{name: SvcECS, sdkID: ecs.ServiceID, iamPrefix: "ecs", ops: []string{
		"ListClusters", "DescribeClusters", "ListServices", "DescribeServices", "DescribeTaskDefinition"}},
	{name: SvcEKS, sdkID: eks.ServiceID, iamPrefix: "eks", ops: []string{
		"ListClusters", "DescribeCluster", "ListNodegroups", "DescribeNodegroup"}},
	{name: SvcLambda, sdkID: lambda.ServiceID, iamPrefix: "lambda", ops: []string{
		"ListFunctions", "ListEventSourceMappings", "ListTags"}},
	{name: SvcECR, sdkID: ecr.ServiceID, iamPrefix: "ecr", ops: []string{"DescribeRepositories", "ListTagsForResource"}},
	{name: SvcSQS, sdkID: sqs.ServiceID, iamPrefix: "sqs", ops: []string{"ListQueues", "GetQueueAttributes", "ListQueueTags"}},
	{name: SvcSNS, sdkID: sns.ServiceID, iamPrefix: "sns", ops: []string{
		"ListTopics", "GetTopicAttributes", "ListSubscriptions", "GetSubscriptionAttributes", "ListTagsForResource"}},
	{name: SvcDynamoDB, sdkID: dynamodb.ServiceID, iamPrefix: "dynamodb", ops: []string{
		"ListTables", "DescribeTable", "DescribeContinuousBackups", "ListTagsOfResource"}},
	{name: SvcElastiCache, sdkID: elasticache.ServiceID, iamPrefix: "elasticache", ops: []string{
		"DescribeReplicationGroups", "DescribeCacheClusters"}},
	{name: SvcKinesis, sdkID: kinesis.ServiceID, iamPrefix: "kinesis", ops: []string{"ListStreams", "DescribeStreamSummary"}},
	{name: SvcRoute53, sdkID: route53.ServiceID, iamPrefix: "route53", ops: []string{"ListHostedZones", "GetHostedZone"}},
	{name: SvcACM, sdkID: acm.ServiceID, iamPrefix: "acm", ops: []string{"ListCertificates", "DescribeCertificate"}},
	{name: SvcIAM, sdkID: iam.ServiceID, iamPrefix: "iam", ops: []string{
		"ListRoles", "ListAttachedRolePolicies", "ListRolePolicies", "ListInstanceProfiles"}},
	{name: SvcSecretsManager, sdkID: secretsmanager.ServiceID, iamPrefix: "secretsmanager", ops: []string{"ListSecrets"}},
	{name: SvcSSM, sdkID: ssm.ServiceID, iamPrefix: "ssm", ops: []string{"DescribeParameters"}},
	{name: SvcKMS, sdkID: kms.ServiceID, iamPrefix: "kms", ops: []string{
		"ListKeys", "DescribeKey", "GetKeyRotationStatus", "ListAliases", "ListResourceTags"}},
	{name: SvcLogs, sdkID: cloudwatchlogs.ServiceID, iamPrefix: "logs", ops: []string{"DescribeLogGroups"}},
	{name: SvcCloudFront, sdkID: cloudfront.ServiceID, iamPrefix: "cloudfront", ops: []string{"ListDistributions"}},
	{name: SvcAPIGatewayV2, sdkID: apigatewayv2.ServiceID, iamPrefix: "apigateway", ops: []string{"GetApis", "GetIntegrations"},
		// API Gateway authorizes by HTTP verb on resource paths; see policy.go.
		actions: map[string]string{"GetApis": "apigateway:GET", "GetIntegrations": "apigateway:GET"}},
	{name: SvcSFN, sdkID: sfn.ServiceID, iamPrefix: "states", ops: []string{"ListStateMachines", "DescribeStateMachine"}},
	{name: SvcEvents, sdkID: eventbridge.ServiceID, iamPrefix: "events", ops: []string{"ListEventBuses", "ListRules", "ListTargetsByRule"}},
	{name: SvcCloudFormation, sdkID: cloudformation.ServiceID, iamPrefix: "cloudformation", ops: []string{"DescribeStacks", "ListStackResources"}},
	{name: SvcCloudControl, sdkID: cloudcontrol.ServiceID, iamPrefix: "cloudformation", ops: []string{"ListResources"}}, // never GetResource: full models can hold secret values (F-06)
}

// Registry returns every operation Hermes discovery may call, sorted by service and name.
func Registry() []Operation {
	var out []Operation
	for _, s := range services {
		for _, op := range s.ops {
			action := s.iamPrefix + ":" + op
			if a, ok := s.actions[op]; ok {
				action = a
			}
			out = append(out, Operation{Service: s.name, ServiceID: s.sdkID, Name: op, IAMAction: action})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ServiceNames returns the Hermes service names that can be selected with --services.
func ServiceNames() []string {
	var out []string
	for _, s := range services {
		if s.name == SvcSTS {
			continue
		}
		out = append(out, s.name)
	}
	sort.Strings(out)
	return out
}

type opKey struct{ serviceID, op string }

var allowed = func() map[opKey]bool {
	m := map[opKey]bool{}
	for _, o := range Registry() {
		m[opKey{o.ServiceID, o.Name}] = true
	}
	return m
}()
