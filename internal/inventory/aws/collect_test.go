package awsinv

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	cctypes "github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

const acct = "123456789012"

func apiErr(code, msg string) error { return &smithy.GenericAPIError{Code: code, Message: msg} }

type fakeSTS struct{}

func (fakeSTS) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return &sts.GetCallerIdentityOutput{Account: aws.String(acct), Arn: aws.String("arn:aws:sts::" + acct + ":assumed-role/HermesDiscoveryReadOnly/hermes-discovery")}, nil
}

type fakeEC2 struct {
	errs map[string]error
}

func (f *fakeEC2) e(op string) error { return f.errs[op] }

func tags(kv ...string) []ec2types.Tag {
	var out []ec2types.Tag
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, ec2types.Tag{Key: aws.String(kv[i]), Value: aws.String(kv[i+1])})
	}
	return out
}

func (f *fakeEC2) DescribeRegions(context.Context, *ec2.DescribeRegionsInput, ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
	return &ec2.DescribeRegionsOutput{Regions: []ec2types.Region{
		{RegionName: aws.String("eu-north-1"), OptInStatus: aws.String("opt-in-not-required")},
		{RegionName: aws.String("af-south-1"), OptInStatus: aws.String("not-opted-in")},
	}}, nil
}
func (f *fakeEC2) DescribeVpcs(context.Context, *ec2.DescribeVpcsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return &ec2.DescribeVpcsOutput{Vpcs: []ec2types.Vpc{{VpcId: aws.String("vpc-1"), CidrBlock: aws.String("10.0.0.0/16"), Tags: tags("Name", "main")}}}, f.e("DescribeVpcs")
}
func (f *fakeEC2) DescribeSubnets(context.Context, *ec2.DescribeSubnetsInput, ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return &ec2.DescribeSubnetsOutput{Subnets: []ec2types.Subnet{{SubnetId: aws.String("subnet-a"), VpcId: aws.String("vpc-1"), CidrBlock: aws.String("10.0.1.0/24"), AvailabilityZone: aws.String("eu-north-1a")}}}, nil
}
func (f *fakeEC2) DescribeSecurityGroups(context.Context, *ec2.DescribeSecurityGroupsInput, ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	return &ec2.DescribeSecurityGroupsOutput{SecurityGroups: []ec2types.SecurityGroup{
		{GroupId: aws.String("sg-app"), GroupName: aws.String("app"), VpcId: aws.String("vpc-1"), IpPermissions: []ec2types.IpPermission{
			{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(443), ToPort: aws.Int32(443), IpRanges: []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}}}}},
		{GroupId: aws.String("sg-db"), GroupName: aws.String("db"), VpcId: aws.String("vpc-1"), IpPermissions: []ec2types.IpPermission{
			{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(5432), ToPort: aws.Int32(5432), UserIdGroupPairs: []ec2types.UserIdGroupPair{{GroupId: aws.String("sg-app")}}}}},
	}}, nil
}
func (f *fakeEC2) DescribeRouteTables(context.Context, *ec2.DescribeRouteTablesInput, ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error) {
	return &ec2.DescribeRouteTablesOutput{RouteTables: []ec2types.RouteTable{{RouteTableId: aws.String("rtb-1"), VpcId: aws.String("vpc-1"),
		Associations: []ec2types.RouteTableAssociation{{SubnetId: aws.String("subnet-a")}},
		Routes:       []ec2types.Route{{DestinationCidrBlock: aws.String("0.0.0.0/0"), NatGatewayId: aws.String("nat-1")}}}}}, nil
}
func (f *fakeEC2) DescribeInternetGateways(context.Context, *ec2.DescribeInternetGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeInternetGatewaysOutput, error) {
	return &ec2.DescribeInternetGatewaysOutput{}, nil
}
func (f *fakeEC2) DescribeNatGateways(context.Context, *ec2.DescribeNatGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeNatGatewaysOutput, error) {
	return &ec2.DescribeNatGatewaysOutput{NatGateways: []ec2types.NatGateway{{NatGatewayId: aws.String("nat-1"), VpcId: aws.String("vpc-1"), SubnetId: aws.String("subnet-a"), State: ec2types.NatGatewayStateAvailable}}}, nil
}
func (f *fakeEC2) DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{
		{InstanceId: aws.String("i-relay"), InstanceType: ec2types.InstanceTypeT3Small, VpcId: aws.String("vpc-1"), SubnetId: aws.String("subnet-a"),
			SourceDestCheck: aws.Bool(false), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}, Tags: tags("Name", "vpn-relay", "role", "wireguard")},
		{InstanceId: aws.String("i-docker"), InstanceType: ec2types.InstanceTypeM5Large, VpcId: aws.String("vpc-1"), SubnetId: aws.String("subnet-a"),
			SourceDestCheck: aws.Bool(true), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
			IamInstanceProfile: &ec2types.IamInstanceProfile{Arn: aws.String("arn:aws:iam::" + acct + ":instance-profile/app")},
			SecurityGroups:     []ec2types.GroupIdentifier{{GroupId: aws.String("sg-app")}}, Tags: tags("Name", "docker-host-1", "runtime", "docker-compose")},
		{InstanceId: aws.String("i-gone"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameTerminated}},
	}}}}, nil
}
func (f *fakeEC2) DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return &ec2.DescribeVolumesOutput{}, f.e("DescribeVolumes")
}
func (f *fakeEC2) DescribeTransitGateways(context.Context, *ec2.DescribeTransitGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeTransitGatewaysOutput, error) {
	return &ec2.DescribeTransitGatewaysOutput{}, nil
}
func (f *fakeEC2) DescribeTransitGatewayAttachments(context.Context, *ec2.DescribeTransitGatewayAttachmentsInput, ...func(*ec2.Options)) (*ec2.DescribeTransitGatewayAttachmentsOutput, error) {
	return &ec2.DescribeTransitGatewayAttachmentsOutput{}, nil
}
func (f *fakeEC2) DescribeVpnConnections(context.Context, *ec2.DescribeVpnConnectionsInput, ...func(*ec2.Options)) (*ec2.DescribeVpnConnectionsOutput, error) {
	// The API returns tunnel pre-shared keys and the customer gateway configuration; they must never be stored.
	return &ec2.DescribeVpnConnectionsOutput{VpnConnections: []ec2types.VpnConnection{{VpnConnectionId: aws.String("vpn-1"), State: ec2types.VpnStateAvailable,
		VpnGatewayId: aws.String("vgw-1"), CustomerGatewayConfiguration: aws.String("<psk>demo-not-a-secret-psk</psk>"),
		Options: &ec2types.VpnConnectionOptions{TunnelOptions: []ec2types.TunnelOption{{PreSharedKey: aws.String("demo-not-a-secret-psk")}}}}}}, nil
}
func (f *fakeEC2) DescribeVpnGateways(context.Context, *ec2.DescribeVpnGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeVpnGatewaysOutput, error) {
	return &ec2.DescribeVpnGatewaysOutput{}, nil
}
func (f *fakeEC2) DescribeVpcEndpoints(context.Context, *ec2.DescribeVpcEndpointsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointsOutput, error) {
	return &ec2.DescribeVpcEndpointsOutput{}, nil
}
func (f *fakeEC2) DescribeVpcPeeringConnections(context.Context, *ec2.DescribeVpcPeeringConnectionsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcPeeringConnectionsOutput, error) {
	return &ec2.DescribeVpcPeeringConnectionsOutput{}, nil
}

type fakeRDS struct{}

func (fakeRDS) DescribeDBInstances(context.Context, *rds.DescribeDBInstancesInput, ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error) {
	return &rds.DescribeDBInstancesOutput{DBInstances: []rdstypes.DBInstance{{
		DBInstanceArn: aws.String("arn:aws:rds:eu-north-1:" + acct + ":db:orders-db"), DBInstanceIdentifier: aws.String("orders-db"),
		Engine: aws.String("postgres"), EngineVersion: aws.String("15.5"), DBInstanceClass: aws.String("db.m6g.large"), MultiAZ: aws.Bool(true),
		AllocatedStorage: aws.Int32(200), StorageEncrypted: aws.Bool(true), KmsKeyId: aws.String("1111-key"), PubliclyAccessible: aws.Bool(false),
		BackupRetentionPeriod: aws.Int32(7), DBParameterGroups: []rdstypes.DBParameterGroupStatus{{DBParameterGroupName: aws.String("pg15-postgis")}},
		VpcSecurityGroups: []rdstypes.VpcSecurityGroupMembership{{VpcSecurityGroupId: aws.String("sg-db")}},
		DBSubnetGroup:     &rdstypes.DBSubnetGroup{DBSubnetGroupName: aws.String("db-subnets"), VpcId: aws.String("vpc-1")},
	}}}, nil
}
func (fakeRDS) DescribeDBClusters(context.Context, *rds.DescribeDBClustersInput, ...func(*rds.Options)) (*rds.DescribeDBClustersOutput, error) {
	return &rds.DescribeDBClustersOutput{}, nil
}
func (fakeRDS) DescribeDBSubnetGroups(context.Context, *rds.DescribeDBSubnetGroupsInput, ...func(*rds.Options)) (*rds.DescribeDBSubnetGroupsOutput, error) {
	return &rds.DescribeDBSubnetGroupsOutput{}, nil
}

type fakeS3 struct{}

func (fakeS3) ListBuckets(context.Context, *s3.ListBucketsInput, ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	return &s3.ListBucketsOutput{Buckets: []s3types.Bucket{{Name: aws.String("flight-logs-example"), BucketRegion: aws.String("eu-north-1")}, {Name: aws.String("elsewhere"), BucketRegion: aws.String("us-west-2")}}}, nil
}
func (fakeS3) GetBucketLocation(context.Context, *s3.GetBucketLocationInput, ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error) {
	return &s3.GetBucketLocationOutput{}, nil
}
func (fakeS3) GetBucketVersioning(context.Context, *s3.GetBucketVersioningInput, ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error) {
	return &s3.GetBucketVersioningOutput{Status: s3types.BucketVersioningStatusEnabled}, nil
}
func (fakeS3) GetBucketEncryption(context.Context, *s3.GetBucketEncryptionInput, ...func(*s3.Options)) (*s3.GetBucketEncryptionOutput, error) {
	return nil, apiErr("ServerSideEncryptionConfigurationNotFoundError", "none")
}
func (fakeS3) GetPublicAccessBlock(context.Context, *s3.GetPublicAccessBlockInput, ...func(*s3.Options)) (*s3.GetPublicAccessBlockOutput, error) {
	t := aws.Bool(true)
	return &s3.GetPublicAccessBlockOutput{PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{BlockPublicAcls: t, IgnorePublicAcls: t, BlockPublicPolicy: t, RestrictPublicBuckets: t}}, nil
}
func (fakeS3) GetBucketOwnershipControls(context.Context, *s3.GetBucketOwnershipControlsInput, ...func(*s3.Options)) (*s3.GetBucketOwnershipControlsOutput, error) {
	return nil, apiErr("OwnershipControlsNotFoundError", "none")
}
func (fakeS3) GetBucketLifecycleConfiguration(context.Context, *s3.GetBucketLifecycleConfigurationInput, ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error) {
	return &s3.GetBucketLifecycleConfigurationOutput{Rules: []s3types.LifecycleRule{{}}}, nil
}
func (fakeS3) GetBucketReplication(context.Context, *s3.GetBucketReplicationInput, ...func(*s3.Options)) (*s3.GetBucketReplicationOutput, error) {
	return nil, apiErr("ReplicationConfigurationNotFoundError", "none")
}
func (fakeS3) GetBucketPolicy(context.Context, *s3.GetBucketPolicyInput, ...func(*s3.Options)) (*s3.GetBucketPolicyOutput, error) {
	return nil, apiErr("AccessDenied", "User: arn:aws:sts::"+acct+":assumed-role/HermesDiscoveryReadOnly/hermes-discovery is not authorized to perform: s3:GetBucketPolicy")
}
func (fakeS3) GetBucketPolicyStatus(context.Context, *s3.GetBucketPolicyStatusInput, ...func(*s3.Options)) (*s3.GetBucketPolicyStatusOutput, error) {
	return nil, apiErr("NoSuchBucketPolicy", "none")
}
func (fakeS3) GetBucketTagging(context.Context, *s3.GetBucketTaggingInput, ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error) {
	return &s3.GetBucketTaggingOutput{TagSet: []s3types.Tag{{Key: aws.String("aws:cloudformation:stack-name"), Value: aws.String("logs-stack")}}}, nil
}

type fakeSQS struct{}

func (fakeSQS) ListQueues(context.Context, *sqs.ListQueuesInput, ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error) {
	return &sqs.ListQueuesOutput{QueueUrls: []string{"https://sqs.eu-north-1.amazonaws.com/" + acct + "/orders", "https://sqs.eu-north-1.amazonaws.com/" + acct + "/orders-dlq", "https://sqs.eu-north-1.amazonaws.com/" + acct + "/jobs.fifo"}}, nil
}
func (fakeSQS) GetQueueAttributes(_ context.Context, in *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	u := aws.ToString(in.QueueUrl)
	name := u[strings.LastIndex(u, "/")+1:]
	at := map[string]string{"QueueArn": "arn:aws:sqs:eu-north-1:" + acct + ":" + name, "VisibilityTimeout": "30", "MessageRetentionPeriod": "1209600"}
	if name == "orders" {
		at["RedrivePolicy"] = `{"deadLetterTargetArn":"arn:aws:sqs:eu-north-1:` + acct + `:orders-dlq","maxReceiveCount":5}`
	}
	if name == "jobs.fifo" {
		at["FifoQueue"] = "true"
	}
	return &sqs.GetQueueAttributesOutput{Attributes: at}, nil
}
func (fakeSQS) ListQueueTags(context.Context, *sqs.ListQueueTagsInput, ...func(*sqs.Options)) (*sqs.ListQueueTagsOutput, error) {
	return &sqs.ListQueueTagsOutput{}, nil
}

type fakeSNS struct{}

var topicARN = "arn:aws:sns:eu-north-1:" + acct + ":telemetry"

func (fakeSNS) ListTopics(context.Context, *sns.ListTopicsInput, ...func(*sns.Options)) (*sns.ListTopicsOutput, error) {
	return &sns.ListTopicsOutput{Topics: []snstypes.Topic{{TopicArn: aws.String(topicARN)}}}, nil
}
func (fakeSNS) GetTopicAttributes(context.Context, *sns.GetTopicAttributesInput, ...func(*sns.Options)) (*sns.GetTopicAttributesOutput, error) {
	return &sns.GetTopicAttributesOutput{Attributes: map[string]string{"SubscriptionsConfirmed": "2"}}, nil
}
func (fakeSNS) ListSubscriptions(context.Context, *sns.ListSubscriptionsInput, ...func(*sns.Options)) (*sns.ListSubscriptionsOutput, error) {
	return &sns.ListSubscriptionsOutput{Subscriptions: []snstypes.Subscription{
		{SubscriptionArn: aws.String(topicARN + ":sub-1"), TopicArn: aws.String(topicARN), Protocol: aws.String("sqs"), Endpoint: aws.String("arn:aws:sqs:eu-north-1:" + acct + ":orders")},
		{SubscriptionArn: aws.String(topicARN + ":sub-2"), TopicArn: aws.String(topicARN), Protocol: aws.String("email"), Endpoint: aws.String("ops@example.com")},
	}}, nil
}
func (fakeSNS) GetSubscriptionAttributes(context.Context, *sns.GetSubscriptionAttributesInput, ...func(*sns.Options)) (*sns.GetSubscriptionAttributesOutput, error) {
	return &sns.GetSubscriptionAttributesOutput{Attributes: map[string]string{"RawMessageDelivery": "true"}}, nil
}
func (fakeSNS) ListTagsForResource(context.Context, *sns.ListTagsForResourceInput, ...func(*sns.Options)) (*sns.ListTagsForResourceOutput, error) {
	return &sns.ListTagsForResourceOutput{}, nil
}

type fakeSecrets struct{}

var secretARN = "arn:aws:secretsmanager:eu-north-1:" + acct + ":secret:db-password-AbCdEf"

func (fakeSecrets) ListSecrets(context.Context, *secretsmanager.ListSecretsInput, ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error) {
	return &secretsmanager.ListSecretsOutput{SecretList: []smtypes.SecretListEntry{{ARN: aws.String(secretARN), Name: aws.String("db-password"), RotationEnabled: aws.Bool(false)}}}, nil
}

type fakeIAM struct{}

func (fakeIAM) ListRoles(context.Context, *iam.ListRolesInput, ...func(*iam.Options)) (*iam.ListRolesOutput, error) {
	trust := `%7B%22Version%22%3A%222012-10-17%22%2C%22Statement%22%3A%5B%7B%22Effect%22%3A%22Allow%22%2C%22Principal%22%3A%7B%22Service%22%3A%22ec2.amazonaws.com%22%7D%2C%22Action%22%3A%22sts%3AAssumeRole%22%7D%5D%7D`
	return &iam.ListRolesOutput{Roles: []iamtypes.Role{
		{RoleName: aws.String("app-host"), Arn: aws.String("arn:aws:iam::" + acct + ":role/app-host"), Path: aws.String("/"), AssumeRolePolicyDocument: aws.String(trust)},
		{RoleName: aws.String("AWSServiceRoleForECS"), Arn: aws.String("arn:aws:iam::" + acct + ":role/aws-service-role/ecs.amazonaws.com/AWSServiceRoleForECS"), Path: aws.String("/aws-service-role/ecs.amazonaws.com/")},
	}}, nil
}
func (fakeIAM) ListAttachedRolePolicies(context.Context, *iam.ListAttachedRolePoliciesInput, ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error) {
	return &iam.ListAttachedRolePoliciesOutput{AttachedPolicies: []iamtypes.AttachedPolicy{{PolicyName: aws.String("AmazonSSMManagedInstanceCore")}}}, nil
}
func (fakeIAM) ListRolePolicies(context.Context, *iam.ListRolePoliciesInput, ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error) {
	return nil, apiErr("AccessDenied", "not authorized to perform iam:ListRolePolicies")
}
func (fakeIAM) ListInstanceProfiles(context.Context, *iam.ListInstanceProfilesInput, ...func(*iam.Options)) (*iam.ListInstanceProfilesOutput, error) {
	return &iam.ListInstanceProfilesOutput{InstanceProfiles: []iamtypes.InstanceProfile{{Arn: aws.String("arn:aws:iam::" + acct + ":instance-profile/app"),
		Roles: []iamtypes.Role{{Arn: aws.String("arn:aws:iam::" + acct + ":role/app-host")}}}}}, nil
}

type fakeECS struct{}

var (
	clusterARN = "arn:aws:ecs:eu-north-1:" + acct + ":cluster/main"
	tdARN      = "arn:aws:ecs:eu-north-1:" + acct + ":task-definition/api:7"
)

func (fakeECS) ListClusters(context.Context, *ecs.ListClustersInput, ...func(*ecs.Options)) (*ecs.ListClustersOutput, error) {
	return &ecs.ListClustersOutput{ClusterArns: []string{clusterARN}}, nil
}
func (fakeECS) DescribeClusters(context.Context, *ecs.DescribeClustersInput, ...func(*ecs.Options)) (*ecs.DescribeClustersOutput, error) {
	return &ecs.DescribeClustersOutput{Clusters: []ecstypes.Cluster{{ClusterArn: aws.String(clusterARN), ClusterName: aws.String("main"), Status: aws.String("ACTIVE")}}}, nil
}
func (fakeECS) ListServices(context.Context, *ecs.ListServicesInput, ...func(*ecs.Options)) (*ecs.ListServicesOutput, error) {
	return &ecs.ListServicesOutput{ServiceArns: []string{"arn:aws:ecs:eu-north-1:" + acct + ":service/main/api"}}, nil
}
func (fakeECS) DescribeServices(context.Context, *ecs.DescribeServicesInput, ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error) {
	return &ecs.DescribeServicesOutput{Services: []ecstypes.Service{{ServiceArn: aws.String("arn:aws:ecs:eu-north-1:" + acct + ":service/main/api"), ServiceName: aws.String("api"),
		ClusterArn: aws.String(clusterARN), DesiredCount: 2, TaskDefinition: aws.String(tdARN), LaunchType: ecstypes.LaunchTypeFargate,
		NetworkConfiguration: &ecstypes.NetworkConfiguration{AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{Subnets: []string{"subnet-a"}, SecurityGroups: []string{"sg-app"}}}}}}, nil
}
func (fakeECS) DescribeTaskDefinition(context.Context, *ecs.DescribeTaskDefinitionInput, ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error) {
	return &ecs.DescribeTaskDefinitionOutput{TaskDefinition: &ecstypes.TaskDefinition{TaskDefinitionArn: aws.String(tdARN), Family: aws.String("api"), Revision: 7,
		Cpu: aws.String("512"), Memory: aws.String("1024"), TaskRoleArn: aws.String("arn:aws:iam::" + acct + ":role/app-host"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{Name: aws.String("api"), Image: aws.String("ghcr.io/example/api:1.4.2"),
			PortMappings: []ecstypes.PortMapping{{ContainerPort: aws.Int32(8080)}},
			Environment:  []ecstypes.KeyValuePair{{Name: aws.String("LOG_LEVEL"), Value: aws.String("demo-env-value-must-not-leak")}},
			Secrets:      []ecstypes.Secret{{Name: aws.String("DATABASE_PASSWORD"), ValueFrom: aws.String("arn:aws:secretsmanager:eu-north-1:" + acct + ":secret:db-password")}}}}}}, nil
}

type fakeCW struct{}

func (fakeCW) GetMetricData(context.Context, *cloudwatch.GetMetricDataInput, ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	return nil, apiErr("AccessDenied", "not authorized to perform cloudwatch:GetMetricData")
}
func (fakeCW) DescribeAlarms(context.Context, *cloudwatch.DescribeAlarmsInput, ...func(*cloudwatch.Options)) (*cloudwatch.DescribeAlarmsOutput, error) {
	return &cloudwatch.DescribeAlarmsOutput{}, nil
}

func fakeFactory(ec2f *fakeEC2) ClientFactory {
	return func(string) *Clients {
		return &Clients{STS: fakeSTS{}, EC2: ec2f, RDS: fakeRDS{}, S3: fakeS3{}, SQS: fakeSQS{}, SNS: fakeSNS{}, SecretsManager: fakeSecrets{}, IAM: fakeIAM{}, ECS: fakeECS{}, CloudWatch: fakeCW{}}
	}
}

func runFake(t *testing.T, ec2f *fakeEC2) *inventory.Inventory {
	t.Helper()
	d := &Discoverer{Clients: fakeFactory(ec2f), Options: Options{
		HomeRegion: "eu-north-1", Services: []string{"ec2", "rds", "s3", "sqs", "sns", "secretsmanager", "iam", "ecs"},
		RoleARN: "arn:aws:iam::" + acct + ":role/HermesDiscoveryReadOnly", ExternalIDUsed: true,
		Now: func() time.Time { return time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC) },
	}}
	inv, err := d.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func hasRel(inv *inventory.Inventory, from, to, typ string) bool {
	for _, r := range inv.Relationships {
		if r.From == from && r.To == to && r.Type == typ {
			return true
		}
	}
	return false
}

func TestDiscoveryWithFakes(t *testing.T) {
	inv := runFake(t, &fakeEC2{errs: map[string]error{"DescribeVolumes": apiErr("UnauthorizedOperation", "You are not authorized to perform this operation.")}})
	if err := inv.Check(); err != nil {
		t.Fatal(err)
	}
	if !inv.Metadata.ReadOnly || inv.Metadata.AccountID != acct || inv.Metadata.Identity == nil || !inv.Metadata.Identity.ExternalIDUsed {
		t.Fatalf("metadata: %+v", inv.Metadata)
	}
	if got := inv.Metadata.Regions; len(got) != 1 || got[0] != "eu-north-1" {
		t.Fatalf("enabled regions should exclude not-opted-in regions: %v", got)
	}
	idx := inv.Index()
	ec2arn := func(k, id string) string { return "arn:aws:ec2:eu-north-1:" + acct + ":" + k + "/" + id }
	relay := idx[ec2arn("instance", "i-relay")]
	docker := idx[ec2arn("instance", "i-docker")]
	if relay == nil || docker == nil {
		t.Fatalf("instances missing: %v", inv.Summary.ByType)
	}
	if idx[ec2arn("instance", "i-gone")] != nil {
		t.Error("terminated instance must be skipped")
	}
	if relay.Attr("source_dest_check") != false || docker.Attr("source_dest_check") != true {
		t.Error("source_dest_check (catalog match key) not emitted correctly")
	}
	if !docker.AttrBool("container_host") || !contains(relay.AttrStrings("workload_hints"), "vpn") {
		t.Errorf("workload hints: docker=%v relay=%v", docker.Attributes["workload_hints"], relay.Attributes["workload_hints"])
	}
	sgDB, sgApp := ec2arn("security-group", "sg-db"), ec2arn("security-group", "sg-app")
	checks := []struct{ from, to, typ string }{
		{ec2arn("subnet", "subnet-a"), ec2arn("vpc", "vpc-1"), "in-vpc"},
		{sgDB, sgApp, "allows-ingress-from"},
		{ec2arn("route-table", "rtb-1"), ec2arn("natgateway", "nat-1"), "routes-to"},
		{docker.ID, "arn:aws:iam::" + acct + ":role/app-host", "assumes-role"}, // resolved through the instance profile
		{"arn:aws:rds:eu-north-1:" + acct + ":db:orders-db", sgDB, "uses-security-group"},
		{"arn:aws:rds:eu-north-1:" + acct + ":db:orders-db", "arn:aws:kms:eu-north-1:" + acct + ":key/1111-key", "encrypted-by"},
		{"arn:aws:sqs:eu-north-1:" + acct + ":orders", topicARN, "subscribes-to"},
		{"arn:aws:sqs:eu-north-1:" + acct + ":orders", "arn:aws:sqs:eu-north-1:" + acct + ":orders-dlq", "depends-on"},
		{tdARN, secretARN, "uses-secret"}, // partial ARN without the random suffix resolved
		{"arn:aws:ecs:eu-north-1:" + acct + ":service/main/api", tdARN, "depends-on"},
	}
	for _, c := range checks {
		if !hasRel(inv, c.from, c.to, c.typ) {
			t.Errorf("missing relationship %s -[%s]-> %s", c.from, c.typ, c.to)
		}
	}
	rdsR := idx["arn:aws:rds:eu-north-1:"+acct+":db:orders-db"]
	if rdsR.AttrString("engine") != "postgres" || !rdsR.AttrBool("multi_az") || rdsR.SizeBytes == nil || *rdsR.SizeBytes != 200<<30 {
		t.Errorf("rds attributes: %+v", rdsR.Attributes)
	}
	fifo := idx["arn:aws:sqs:eu-north-1:"+acct+":jobs.fifo"]
	if fifo == nil || fifo.Attr("fifo_queue") != true {
		t.Error("fifo_queue (catalog match key) not emitted")
	}
	// Bucket in an out-of-scope region is skipped; the denied GetBucketPolicy makes the bucket partial.
	b := idx["arn:aws:s3:::flight-logs-example"]
	if b == nil || b.Completeness != "partial" || idx["arn:aws:s3:::elsewhere"] != nil {
		t.Fatalf("bucket: %+v", b)
	}
	if b.ManagedBy != "cloudformation" {
		t.Errorf("managed_by from CloudFormation tag: %q", b.ManagedBy)
	}
	role := idx["arn:aws:iam::"+acct+":role/app-host"]
	if role == nil || role.Completeness != "partial" || !contains(role.AttrStrings("trust_services"), "ec2.amazonaws.com") {
		t.Errorf("role: %+v", role)
	}
	if role.Attr("attached_policy_names") != nil {
		t.Error("policy names must be omitted (not guessed) when a listing call failed")
	}
	for _, r := range inv.Resources {
		if strings.Contains(r.ID, "aws-service-role") {
			t.Error("service-linked roles must be skipped")
		}
	}
	// Gaps: denied calls are recorded with a classified reason and discovery continues.
	reasons := map[string]string{}
	for _, g := range inv.Gaps {
		reasons[g.Operation] = g.Reason
	}
	for op, want := range map[string]string{"DescribeVolumes": "access-denied", "GetBucketPolicy": "access-denied", "GetMetricData": "access-denied", "ListRolePolicies": "access-denied"} {
		if reasons[op] != want {
			t.Errorf("gap for %s: got %q want %q (gaps=%v)", op, reasons[op], want, reasons)
		}
	}
	// Secret values, env values, VPN pre-shared keys and email endpoints never appear.
	raw, _ := json.Marshal(inv)
	for _, leak := range []string{"demo-env-value-must-not-leak", "demo-not-a-secret-psk", "ops@example.com"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("inventory leaks %q", leak)
		}
	}
	td := idx[tdARN]
	cont := td.Attributes["containers"].([]map[string]any)[0]
	if names := cont["environment_names"].([]string); len(names) != 1 || names[0] != "LOG_LEVEL" {
		t.Errorf("env names: %v", cont["environment_names"])
	}
}

func TestDiscoveryRedact(t *testing.T) {
	inv := runFake(t, &fakeEC2{})
	inventory.Redact(inv)
	raw, _ := json.Marshal(inv)
	if strings.Contains(string(raw), acct) {
		t.Fatal("redacted inventory still contains the account id")
	}
}

func TestDiscoveryRejectsUnknownService(t *testing.T) {
	d := &Discoverer{Clients: fakeFactory(&fakeEC2{}), Options: Options{Services: []string{"ec3"}}}
	if _, err := d.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "unknown service") {
		t.Fatalf("want unknown service error, got %v", err)
	}
}

// fakeCloudControl returns models full of secret VALUES under keys a name filter would not
// catch. GetResource is implemented (the full model) but must never be called (F-06).
type fakeCloudControl struct{ getResourceCalls *int }

const ccCanaryEnv, ccCanaryUserData, ccCanarySSM = "CC-ENV-CANARY-demo-not-a-secret", "CC-USERDATA-CANARY-demo-not-a-secret", "CC-SSM-CANARY-demo-not-a-secret"

func (f fakeCloudControl) ListResources(_ context.Context, in *cloudcontrol.ListResourcesInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.ListResourcesOutput, error) {
	props := map[string]string{
		"AWS::ECS::TaskDefinition": `{"TaskDefinitionArn":"arn:aws:ecs:eu-north-1:123456789012:task-definition/api:7","ContainerDefinitions":[{"Name":"api","Environment":[{"Name":"DATABASE_URL","Value":"postgres://app:` + ccCanaryEnv + `@db/app"}]}],"Tags":[{"Key":"team","Value":"fleet"}]}`,
		"AWS::EC2::LaunchTemplate": `{"LaunchTemplateId":"lt-0123","LaunchTemplateData":{"UserData":"` + ccCanaryUserData + `"},"Tags":{"team":"fleet"}}`,
		"AWS::SSM::Parameter":      `{"Name":"/app/api-key","Type":"String","Value":"` + ccCanarySSM + `"}`,
	}[aws.ToString(in.TypeName)]
	return &cloudcontrol.ListResourcesOutput{ResourceDescriptions: []cctypes.ResourceDescription{{Identifier: aws.String("res-1"), Properties: aws.String(props)}}}, nil
}

func (f fakeCloudControl) GetResource(context.Context, *cloudcontrol.GetResourceInput, ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceOutput, error) {
	*f.getResourceCalls++
	return &cloudcontrol.GetResourceOutput{ResourceDescription: &cctypes.ResourceDescription{Identifier: aws.String("res-1"),
		Properties: aws.String(`{"Environment":{"Variables":{"API_KEY":"` + ccCanaryEnv + `"}},"UserData":"` + ccCanaryUserData + `","Value":"` + ccCanarySSM + `"}`)}}, nil
}

func TestCloudControlFallbackStoresIdentifiersAndTagsOnly(t *testing.T) {
	calls := 0
	d := &Discoverer{Clients: func(string) *Clients {
		return &Clients{STS: fakeSTS{}, EC2: &fakeEC2{}, CloudControl: fakeCloudControl{getResourceCalls: &calls}}
	}, Options: Options{
		HomeRegion: "eu-north-1", Services: []string{SvcCloudControl},
		CloudControlTypes: []string{"AWS::ECS::TaskDefinition", "AWS::EC2::LaunchTemplate", "AWS::SSM::Parameter"},
		Now:               func() time.Time { return time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC) },
	}}
	inv, err := d.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Errorf("GetResource (the full model) must never be called, got %d calls", calls)
	}
	raw, _ := json.Marshal(inv)
	for _, leak := range []string{ccCanaryEnv, ccCanaryUserData, ccCanarySSM, "DATABASE_URL", "LaunchTemplateData", "ContainerDefinitions"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("inventory contains %q: Cloud Control properties must not be stored", leak)
		}
	}
	if len(inv.Resources) != 3 {
		t.Fatalf("want 3 resources, got %d", len(inv.Resources))
	}
	for _, r := range inv.Resources {
		if r.Completeness != inventory.CompletenessIdentifierOnly || len(r.Attributes) != 1 || r.Attributes["cloudcontrol_identifier"] != "res-1" || r.Region != "eu-north-1" {
			t.Errorf("%s: want identifier-only with the identifier as the only attribute: %+v", r.Type, r)
		}
		if r.Type != "AWS::SSM::Parameter" && r.Tags["team"] != "fleet" {
			t.Errorf("%s: tags (list or map form) must be kept: %v", r.Type, r.Tags)
		}
	}
	if !strings.Contains(string(raw), "arn:aws:ecs:eu-north-1:123456789012:task-definition/api:7") && !strings.Contains(string(raw), "AWS::ECS::TaskDefinition|eu-north-1|res-1") {
		t.Errorf("the resource id must be the ARN or type|region|identifier: %s", raw)
	}
}

// fakeLambda returns a function whose environment holds a canary value (F-06 audit).
type fakeLambda struct{}

const lambdaCanary = "LAMBDA-ENV-CANARY-demo-not-a-secret"

func (fakeLambda) ListFunctions(context.Context, *lambda.ListFunctionsInput, ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error) {
	return &lambda.ListFunctionsOutput{Functions: []lambdatypes.FunctionConfiguration{{
		FunctionName: aws.String("ingest"), FunctionArn: aws.String("arn:aws:lambda:eu-north-1:" + acct + ":function:ingest"),
		Runtime: lambdatypes.RuntimePython312, Role: aws.String("arn:aws:iam::" + acct + ":role/ingest"),
		Environment: &lambdatypes.EnvironmentResponse{Variables: map[string]string{"API_KEY": lambdaCanary, "DATABASE_URL": "postgres://app:" + lambdaCanary + "@db/app"}},
	}}}, nil
}
func (fakeLambda) ListEventSourceMappings(context.Context, *lambda.ListEventSourceMappingsInput, ...func(*lambda.Options)) (*lambda.ListEventSourceMappingsOutput, error) {
	return &lambda.ListEventSourceMappingsOutput{}, nil
}
func (fakeLambda) ListTags(context.Context, *lambda.ListTagsInput, ...func(*lambda.Options)) (*lambda.ListTagsOutput, error) {
	return &lambda.ListTagsOutput{}, nil
}

// F-06 audit: the Lambda collector keeps environment variable NAMES only.
func TestLambdaEnvironmentValuesNeverStored(t *testing.T) {
	d := &Discoverer{Clients: func(string) *Clients {
		return &Clients{STS: fakeSTS{}, EC2: &fakeEC2{}, Lambda: fakeLambda{}}
	}, Options: Options{HomeRegion: "eu-north-1", Services: []string{SvcLambda},
		Now: func() time.Time { return time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC) }}}
	inv, err := d.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(inv)
	if strings.Contains(string(raw), lambdaCanary) {
		t.Fatalf("Lambda environment values reached the inventory: %s", raw)
	}
	if len(inv.Resources) != 1 {
		t.Fatalf("resources: %+v", inv.Resources)
	}
	names, _ := inv.Resources[0].Attributes["environment_names"].([]string)
	sort.Strings(names)
	if strings.Join(names, ",") != "API_KEY,DATABASE_URL" {
		t.Errorf("environment names must be kept: %v", inv.Resources[0].Attributes["environment_names"])
	}
}

// F-06 audit: no registered operation returns EC2 user data or launch-template data, and the
// ECS (TestDiscoveryWithFakes) and Lambda (TestLambdaEnvironmentValuesNeverStored) collectors
// keep environment variable names only.
func TestRegistryHasNoUserDataOperations(t *testing.T) {
	for _, op := range []string{"DescribeInstanceAttribute", "DescribeLaunchTemplateVersions", "GetLaunchTemplateData", "GetResource"} {
		for _, sv := range services {
			for _, o := range sv.ops {
				if o == op {
					t.Errorf("%s:%s returns user data / full models and must not be in the registry", sv.name, op)
				}
			}
		}
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
