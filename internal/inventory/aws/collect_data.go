package awsinv

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

func rdsTags(tags []rdstypes.Tag) map[string]string {
	return tagMap(tags, func(t rdstypes.Tag) (*string, *string) { return t.Key, t.Value })
}

func collectRDS(ctx context.Context, s *scope) {
	c := s.c.RDS
	const svc = SvcRDS
	{
		p := rds.NewDescribeDBInstancesPaginator(c, &rds.DescribeDBInstancesInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeDBInstances", err, "RDS database instances (engines, versions, sizes, HA) were not inventoried", "")
				break
			}
			for _, d := range page.DBInstances {
				arn := aws.ToString(d.DBInstanceArn)
				ident := aws.ToString(d.DBInstanceIdentifier)
				a := attrs{}.s("identifier", ident).str("engine", d.Engine).str("engine_version", d.EngineVersion).
					str("instance_class", d.DBInstanceClass).b("multi_az", d.MultiAZ).i32("allocated_storage_gb", d.AllocatedStorage).
					i32("max_allocated_storage_gb", d.MaxAllocatedStorage).str("storage_type", d.StorageType).i32("iops", d.Iops).
					b("storage_encrypted", d.StorageEncrypted).str("kms_key_id", d.KmsKeyId).b("publicly_accessible", d.PubliclyAccessible).
					i32("backup_retention_days", d.BackupRetentionPeriod).b("iam_database_authentication_enabled", d.IAMDatabaseAuthenticationEnabled).
					str("availability_zone", d.AvailabilityZone).str("secondary_availability_zone", d.SecondaryAvailabilityZone).
					b("deletion_protection", d.DeletionProtection).b("performance_insights_enabled", d.PerformanceInsightsEnabled).
					str("cluster_identifier", d.DBClusterIdentifier).str("read_replica_source", d.ReadReplicaSourceDBInstanceIdentifier).
					str("status", d.DBInstanceStatus).str("ca_certificate_identifier", d.CACertificateIdentifier).
					b("auto_minor_version_upgrade", d.AutoMinorVersionUpgrade).s("size_basis", "provisioned")
				a.list("read_replica_identifiers", d.ReadReplicaDBInstanceIdentifiers)
				var pgs, ogs, sgs []string
				for _, pg := range d.DBParameterGroups {
					pgs = append(pgs, aws.ToString(pg.DBParameterGroupName))
				}
				for _, og := range d.OptionGroupMemberships {
					ogs = append(ogs, aws.ToString(og.OptionGroupName))
				}
				for _, g := range d.VpcSecurityGroups {
					sgs = append(sgs, aws.ToString(g.VpcSecurityGroupId))
					s.rel(arn, s.ec2ARN("security-group", aws.ToString(g.VpcSecurityGroupId)), inventory.RelUsesSecurityGroup)
				}
				a.list("parameter_groups", pgs).list("option_groups", ogs).list("security_group_ids", sgs)
				if d.Endpoint != nil {
					a.str("endpoint_address", d.Endpoint.Address).i32("port", d.Endpoint.Port)
				}
				if sg := d.DBSubnetGroup; sg != nil {
					a.str("db_subnet_group", sg.DBSubnetGroupName).str("vpc_id", sg.VpcId)
					s.rel(arn, s.ec2ARN("vpc", aws.ToString(sg.VpcId)), inventory.RelInVPC)
					s.rel(arn, s.arn("rds", "subgrp:"+aws.ToString(sg.DBSubnetGroupName)), inventory.RelDependsOn)
				}
				if k := aws.ToString(d.KmsKeyId); k != "" {
					s.rel(arn, s.kmsRef(k), inventory.RelEncryptedBy)
				}
				if d.MasterUserSecret != nil {
					a.any("master_user_secret_managed", true)
					s.rel(arn, aws.ToString(d.MasterUserSecret.SecretArn), inventory.RelUsesSecret)
				}
				if cl := aws.ToString(d.DBClusterIdentifier); cl != "" {
					s.rel(arn, s.arn("rds", "cluster:"+cl), inventory.RelDependsOn)
				}
				if src := aws.ToString(d.ReadReplicaSourceDBInstanceIdentifier); src != "" {
					if !strings.HasPrefix(src, "arn:") {
						src = s.arn("rds", "db:"+src)
					}
					s.rel(arn, src, inventory.RelDependsOn)
				}
				r := inventory.Resource{ID: arn, Type: "AWS::RDS::DBInstance", Name: ident, Tags: rdsTags(d.TagList), Attributes: a.m()}
				if d.AllocatedStorage != nil && aws.ToString(d.DBClusterIdentifier) == "" {
					r.SizeBytes = inventory.Int64(int64(*d.AllocatedStorage) << 30)
				}
				s.add(r)
			}
		}
	}
	{
		p := rds.NewDescribeDBClustersPaginator(c, &rds.DescribeDBClustersInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeDBClusters", err, "Aurora/Multi-AZ DB clusters were not inventoried", "")
				break
			}
			for _, d := range page.DBClusters {
				arn := aws.ToString(d.DBClusterArn)
				ident := aws.ToString(d.DBClusterIdentifier)
				a := attrs{}.s("identifier", ident).str("engine", d.Engine).str("engine_version", d.EngineVersion).
					str("engine_mode", d.EngineMode).b("multi_az", d.MultiAZ).b("storage_encrypted", d.StorageEncrypted).
					str("kms_key_id", d.KmsKeyId).b("iam_database_authentication_enabled", d.IAMDatabaseAuthenticationEnabled).
					i32("backup_retention_days", d.BackupRetentionPeriod).b("deletion_protection", d.DeletionProtection).
					str("db_subnet_group", d.DBSubnetGroup).str("parameter_group", d.DBClusterParameterGroup).i32("port", d.Port).
					i32("allocated_storage_gb", d.AllocatedStorage).str("status", d.Status).str("instance_class", d.DBClusterInstanceClass)
				var members, sgs []string
				for _, m := range d.DBClusterMembers {
					members = append(members, aws.ToString(m.DBInstanceIdentifier))
				}
				for _, g := range d.VpcSecurityGroups {
					sgs = append(sgs, aws.ToString(g.VpcSecurityGroupId))
					s.rel(arn, s.ec2ARN("security-group", aws.ToString(g.VpcSecurityGroupId)), inventory.RelUsesSecurityGroup)
				}
				a.list("members", members).list("security_group_ids", sgs)
				if sc := d.ServerlessV2ScalingConfiguration; sc != nil {
					a.any("serverless_v2", map[string]any{"min_capacity": aws.ToFloat64(sc.MinCapacity), "max_capacity": aws.ToFloat64(sc.MaxCapacity)})
				}
				if k := aws.ToString(d.KmsKeyId); k != "" {
					s.rel(arn, s.kmsRef(k), inventory.RelEncryptedBy)
				}
				if d.MasterUserSecret != nil {
					s.rel(arn, aws.ToString(d.MasterUserSecret.SecretArn), inventory.RelUsesSecret)
				}
				if sg := aws.ToString(d.DBSubnetGroup); sg != "" {
					s.rel(arn, s.arn("rds", "subgrp:"+sg), inventory.RelDependsOn)
				}
				s.add(inventory.Resource{ID: arn, Type: "AWS::RDS::DBCluster", Name: ident, Tags: rdsTags(d.TagList), Attributes: a.m()})
			}
		}
	}
	{
		p := rds.NewDescribeDBSubnetGroupsPaginator(c, &rds.DescribeDBSubnetGroupsInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeDBSubnetGroups", err, "DB subnet groups were not inventoried", "")
				break
			}
			for _, g := range page.DBSubnetGroups {
				name := aws.ToString(g.DBSubnetGroupName)
				arn := firstNonEmpty(aws.ToString(g.DBSubnetGroupArn), s.arn("rds", "subgrp:"+name))
				var subnets []string
				for _, sn := range g.Subnets {
					id := aws.ToString(sn.SubnetIdentifier)
					subnets = append(subnets, id)
					s.rel(arn, s.ec2ARN("subnet", id), inventory.RelInSubnet)
				}
				s.rel(arn, s.ec2ARN("vpc", aws.ToString(g.VpcId)), inventory.RelInVPC)
				s.add(inventory.Resource{ID: arn, Type: "AWS::RDS::DBSubnetGroup", Name: name,
					Attributes: attrs{}.s("identifier", name).str("vpc_id", g.VpcId).list("subnet_ids", subnets).m()})
			}
		}
	}
}

func collectDynamoDB(ctx context.Context, s *scope) {
	c := s.c.DynamoDB
	const svc = SvcDynamoDB
	p := dynamodb.NewListTablesPaginator(c, &dynamodb.ListTablesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListTables", err, "DynamoDB tables were not inventoried", "")
			return
		}
		for _, name := range page.TableNames {
			arn := s.arn("dynamodb", "table/"+name)
			out, err := c.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(name)})
			if err != nil {
				s.gap(svc, "DescribeTable", err, "Table configuration and size unknown", arn)
				s.add(inventory.Resource{ID: arn, Type: "AWS::DynamoDB::Table", Name: name, Completeness: inventory.CompletenessIdentifierOnly})
				continue
			}
			t := out.Table
			arn = firstNonEmpty(aws.ToString(t.TableArn), arn)
			a := attrs{}.s("table_name", name).s("table_status", string(t.TableStatus)).i64("item_count", t.ItemCount).
				any("gsi_count", int64(len(t.GlobalSecondaryIndexes))).any("lsi_count", int64(len(t.LocalSecondaryIndexes))).
				b("deletion_protection", t.DeletionProtectionEnabled)
			billing := "PROVISIONED"
			if t.BillingModeSummary != nil && t.BillingModeSummary.BillingMode != "" {
				billing = string(t.BillingModeSummary.BillingMode)
			}
			a.s("billing_mode", billing)
			var keys []map[string]any
			for _, k := range t.KeySchema {
				keys = append(keys, map[string]any{"name": aws.ToString(k.AttributeName), "key_type": string(k.KeyType)})
			}
			a.any("key_schema", keys)
			if t.StreamSpecification != nil {
				a.b("stream_enabled", t.StreamSpecification.StreamEnabled).s("stream_view_type", string(t.StreamSpecification.StreamViewType))
			}
			if t.SSEDescription != nil {
				a.s("sse_type", string(t.SSEDescription.SSEType)).str("kms_key_arn", t.SSEDescription.KMSMasterKeyArn)
				if k := aws.ToString(t.SSEDescription.KMSMasterKeyArn); k != "" {
					s.rel(arn, k, inventory.RelEncryptedBy)
				}
			}
			if t.TableClassSummary != nil {
				a.s("table_class", string(t.TableClassSummary.TableClass))
			}
			var replicas []string
			for _, r := range t.Replicas {
				replicas = append(replicas, aws.ToString(r.RegionName))
			}
			a.list("replica_regions", replicas)
			r := inventory.Resource{ID: arn, Type: "AWS::DynamoDB::Table", Name: name, Attributes: a.m(), SizeBytes: t.TableSizeBytes}
			if cb, err := c.DescribeContinuousBackups(ctx, &dynamodb.DescribeContinuousBackupsInput{TableName: aws.String(name)}); err != nil {
				s.gap(svc, "DescribeContinuousBackups", err, "Point-in-time recovery status unknown", arn)
			} else if cb.ContinuousBackupsDescription != nil && cb.ContinuousBackupsDescription.PointInTimeRecoveryDescription != nil {
				r.Attributes["pitr_enabled"] = cb.ContinuousBackupsDescription.PointInTimeRecoveryDescription.PointInTimeRecoveryStatus == ddbtypes.PointInTimeRecoveryStatusEnabled
			}
			if tg, err := c.ListTagsOfResource(ctx, &dynamodb.ListTagsOfResourceInput{ResourceArn: aws.String(arn)}); err != nil {
				s.gap(svc, "ListTagsOfResource", err, "Tags unknown", arn)
			} else {
				r.Tags = tagMap(tg.Tags, func(t ddbtypes.Tag) (*string, *string) { return t.Key, t.Value })
			}
			s.add(r)
		}
	}
}

func collectElastiCache(ctx context.Context, s *scope) {
	c := s.c.ElastiCache
	const svc = SvcElastiCache
	inGroup := map[string]bool{}
	{
		p := elasticache.NewDescribeReplicationGroupsPaginator(c, &elasticache.DescribeReplicationGroupsInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeReplicationGroups", err, "ElastiCache replication groups (Redis/Valkey) were not inventoried", "")
				break
			}
			for _, g := range page.ReplicationGroups {
				id := aws.ToString(g.ReplicationGroupId)
				arn := firstNonEmpty(aws.ToString(g.ARN), s.arn("elasticache", "replicationgroup:"+id))
				a := attrs{}.s("resource_id", id).str("engine", g.Engine).str("node_type", g.CacheNodeType).
					b("cluster_enabled", g.ClusterEnabled).s("multi_az", string(g.MultiAZ)).s("automatic_failover", string(g.AutomaticFailover)).
					b("at_rest_encryption_enabled", g.AtRestEncryptionEnabled).b("transit_encryption_enabled", g.TransitEncryptionEnabled).
					b("auth_token_enabled", g.AuthTokenEnabled).str("kms_key_id", g.KmsKeyId).str("status", g.Status).
					any("node_group_count", int64(len(g.NodeGroups)))
				if aws.ToString(g.Engine) == "" {
					a["engine"] = "redis"
				}
				a.list("member_clusters", g.MemberClusters)
				for _, m := range g.MemberClusters {
					inGroup[m] = true
				}
				if k := aws.ToString(g.KmsKeyId); k != "" {
					s.rel(arn, s.kmsRef(k), inventory.RelEncryptedBy)
				}
				s.add(inventory.Resource{ID: arn, Type: "AWS::ElastiCache::ReplicationGroup", Name: id, Attributes: a.m()})
			}
		}
	}
	p := elasticache.NewDescribeCacheClustersPaginator(c, &elasticache.DescribeCacheClustersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "DescribeCacheClusters", err, "ElastiCache clusters (Memcached, standalone Redis) were not inventoried", "")
			return
		}
		for _, cc := range page.CacheClusters {
			id := aws.ToString(cc.CacheClusterId)
			if inGroup[id] || aws.ToString(cc.ReplicationGroupId) != "" {
				continue // represented by its replication group
			}
			arn := firstNonEmpty(aws.ToString(cc.ARN), s.arn("elasticache", "cluster:"+id))
			a := attrs{}.s("resource_id", id).str("engine", cc.Engine).str("engine_version", cc.EngineVersion).
				str("node_type", cc.CacheNodeType).i32("num_cache_nodes", cc.NumCacheNodes).str("subnet_group", cc.CacheSubnetGroupName).
				b("at_rest_encryption_enabled", cc.AtRestEncryptionEnabled).b("transit_encryption_enabled", cc.TransitEncryptionEnabled).
				str("status", cc.CacheClusterStatus)
			for _, g := range cc.SecurityGroups {
				s.rel(arn, s.ec2ARN("security-group", aws.ToString(g.SecurityGroupId)), inventory.RelUsesSecurityGroup)
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::ElastiCache::CacheCluster", Name: id, Attributes: a.m()})
		}
	}
}

// --- S3 ---------------------------------------------------------------------------------

func normalizeBucketRegion(loc string) string {
	switch loc {
	case "":
		return "us-east-1"
	case "EU":
		return "eu-west-1"
	}
	return loc
}

func collectS3(ctx context.Context, s *scope) {
	const svc = SvcS3
	var buckets []s3types.Bucket
	p := s3.NewListBucketsPaginator(s.c.S3, &s3.ListBucketsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListBuckets", err, "S3 buckets were not inventoried", "")
			return
		}
		buckets = append(buckets, page.Buckets...)
	}
	byRegion := map[string][]string{}
	skipped := 0
	for _, b := range buckets {
		name := aws.ToString(b.Name)
		arn := fmt.Sprintf("arn:%s:s3:::%s", s.partition, name)
		region := aws.ToString(b.BucketRegion)
		if region == "" {
			out, err := s.c.S3.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String(name)})
			if err != nil {
				s.gap(svc, "GetBucketLocation", err, "Bucket region unknown; bucket details not collected", arn)
				s.add(inventory.Resource{ID: arn, Type: "AWS::S3::Bucket", Name: name, Region: "unknown", Completeness: inventory.CompletenessIdentifierOnly})
				continue
			}
			region = normalizeBucketRegion(string(out.LocationConstraint))
		}
		if !s.regionSet[region] {
			skipped++
			continue
		}
		bs := &scope{region: region, account: s.account, partition: s.partition, c: s.clientsFor(region), sink: s.sink}
		collectBucket(ctx, bs, name, arn)
		byRegion[region] = append(byRegion[region], name)
	}
	if skipped > 0 {
		s.note("s3: %d bucket(s) in regions outside the discovery scope were skipped", skipped)
	}
	for region, names := range byRegion {
		bs := &scope{region: region, account: s.account, partition: s.partition, c: s.clientsFor(region), sink: s.sink}
		collectBucketSizes(ctx, bs, names)
	}
}

func collectBucket(ctx context.Context, s *scope, name, arn string) {
	const svc = SvcS3
	c := s.c.S3
	b := aws.String(name)
	a := attrs{}.s("bucket_name", name)
	var tags map[string]string

	if v, err := c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: b}); err != nil {
		s.gap(svc, "GetBucketVersioning", err, "Versioning status unknown", arn)
	} else {
		status := string(v.Status)
		if status == "" {
			status = "Disabled"
		}
		a.s("versioning", status).any("mfa_delete", v.MFADelete == s3types.MFADeleteStatusEnabled)
	}
	if e, err := c.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: b}); err != nil {
		if isNotFound(err, "ServerSideEncryptionConfigurationNotFoundError") {
			a.s("sse_algorithm", "none")
		} else {
			s.gap(svc, "GetBucketEncryption", err, "Default encryption unknown", arn)
		}
	} else if e.ServerSideEncryptionConfiguration != nil {
		for _, r := range e.ServerSideEncryptionConfiguration.Rules {
			if d := r.ApplyServerSideEncryptionByDefault; d != nil {
				a.s("sse_algorithm", string(d.SSEAlgorithm)).str("kms_key_id", d.KMSMasterKeyID).b("bucket_key_enabled", r.BucketKeyEnabled)
				if k := aws.ToString(d.KMSMasterKeyID); k != "" {
					s.rel(arn, s.kmsRef(k), inventory.RelEncryptedBy)
				}
			}
		}
	}
	if pab, err := c.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{Bucket: b}); err != nil {
		if isNotFound(err, "NoSuchPublicAccessBlockConfiguration") {
			a.any("public_access_fully_blocked", false).any("public_access_block_configured", false)
		} else {
			s.gap(svc, "GetPublicAccessBlock", err, "Public access block settings unknown", arn)
		}
	} else if cfg := pab.PublicAccessBlockConfiguration; cfg != nil {
		all := aws.ToBool(cfg.BlockPublicAcls) && aws.ToBool(cfg.IgnorePublicAcls) && aws.ToBool(cfg.BlockPublicPolicy) && aws.ToBool(cfg.RestrictPublicBuckets)
		a.any("public_access_block_configured", true).any("public_access_fully_blocked", all).
			any("public_access_block", map[string]any{
				"block_public_acls": aws.ToBool(cfg.BlockPublicAcls), "ignore_public_acls": aws.ToBool(cfg.IgnorePublicAcls),
				"block_public_policy": aws.ToBool(cfg.BlockPublicPolicy), "restrict_public_buckets": aws.ToBool(cfg.RestrictPublicBuckets)})
	}
	if oc, err := c.GetBucketOwnershipControls(ctx, &s3.GetBucketOwnershipControlsInput{Bucket: b}); err != nil {
		if !isNotFound(err, "OwnershipControlsNotFoundError") {
			s.gap(svc, "GetBucketOwnershipControls", err, "Object ownership setting unknown", arn)
		}
	} else if oc.OwnershipControls != nil && len(oc.OwnershipControls.Rules) > 0 {
		a.s("object_ownership", string(oc.OwnershipControls.Rules[0].ObjectOwnership))
	}
	if lc, err := c.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: b}); err != nil {
		if isNotFound(err, "NoSuchLifecycleConfiguration") {
			a.any("lifecycle_rule_count", int64(0))
		} else {
			s.gap(svc, "GetBucketLifecycleConfiguration", err, "Lifecycle rules unknown", arn)
		}
	} else {
		a.any("lifecycle_rule_count", int64(len(lc.Rules)))
	}
	if rep, err := c.GetBucketReplication(ctx, &s3.GetBucketReplicationInput{Bucket: b}); err != nil {
		if isNotFound(err, "ReplicationConfigurationNotFoundError") {
			a.any("replication_enabled", false)
		} else {
			s.gap(svc, "GetBucketReplication", err, "Replication configuration unknown", arn)
		}
	} else if rep.ReplicationConfiguration != nil {
		a.any("replication_enabled", len(rep.ReplicationConfiguration.Rules) > 0).any("replication_rule_count", int64(len(rep.ReplicationConfiguration.Rules)))
		for _, r := range rep.ReplicationConfiguration.Rules {
			if r.Destination != nil {
				s.rel(arn, aws.ToString(r.Destination.Bucket), inventory.RelWritesTo)
			}
		}
	}
	if pol, err := c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: b}); err != nil {
		if isNotFound(err, "NoSuchBucketPolicy") {
			a.any("has_bucket_policy", false)
		} else {
			s.gap(svc, "GetBucketPolicy", err, "Bucket policy flags unknown", arn)
		}
	} else {
		wild, tls, cross := bucketPolicyFlags(aws.ToString(pol.Policy), s.account)
		a.any("has_bucket_policy", true).any("policy_allows_wildcard_principal", wild).
			any("policy_denies_insecure_transport", tls).any("policy_cross_account_principal_count", int64(cross))
	}
	if ps, err := c.GetBucketPolicyStatus(ctx, &s3.GetBucketPolicyStatusInput{Bucket: b}); err != nil {
		if !isNotFound(err, "NoSuchBucketPolicy") {
			s.gap(svc, "GetBucketPolicyStatus", err, "Public policy status unknown", arn)
		}
	} else if ps.PolicyStatus != nil {
		a.b("policy_is_public", ps.PolicyStatus.IsPublic)
	}
	if tg, err := c.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: b}); err != nil {
		if !isNotFound(err, "NoSuchTagSet", "NoSuchTagSetError") {
			s.gap(svc, "GetBucketTagging", err, "Tags unknown", arn)
		}
	} else {
		tags = tagMap(tg.TagSet, func(t s3types.Tag) (*string, *string) { return t.Key, t.Value })
	}
	s.add(inventory.Resource{ID: arn, Type: "AWS::S3::Bucket", Name: name, Region: s.region, Tags: tags, Attributes: a.m()})
}

// s3StorageTypes are the CloudWatch StorageType dimensions summed for BucketSizeBytes.
var s3StorageTypes = []string{"StandardStorage", "StandardIAStorage", "OneZoneIAStorage", "ReducedRedundancyStorage",
	"IntelligentTieringFAStorage", "IntelligentTieringIAStorage", "IntelligentTieringAAStorage",
	"GlacierInstantRetrievalStorage", "GlacierStorage", "DeepArchiveStorage"}

// collectBucketSizes reads the daily S3 storage metrics (BucketSizeBytes, NumberOfObjects)
// from CloudWatch. When denied, the sizes are a recorded gap (transfer estimates exclude S3).
func collectBucketSizes(ctx context.Context, s *scope, names []string) {
	end := time.Now().UTC().Truncate(24 * time.Hour)
	start := end.Add(-3 * 24 * time.Hour)
	type key struct{ bucket, metric string }
	ids := map[string]key{}
	var queries []cwtypes.MetricDataQuery
	for bi, name := range names {
		add := func(metric, storage string, si int) {
			id := fmt.Sprintf("m%d_%d", bi, si)
			ids[id] = key{name, metric}
			queries = append(queries, cwtypes.MetricDataQuery{
				Id: aws.String(id),
				MetricStat: &cwtypes.MetricStat{
					Metric: &cwtypes.Metric{Namespace: aws.String("AWS/S3"), MetricName: aws.String(metric), Dimensions: []cwtypes.Dimension{
						{Name: aws.String("BucketName"), Value: aws.String(name)}, {Name: aws.String("StorageType"), Value: aws.String(storage)}}},
					Period: aws.Int32(86400), Stat: aws.String("Average"),
				},
			})
		}
		for si, st := range s3StorageTypes {
			add("BucketSizeBytes", st, si)
		}
		add("NumberOfObjects", "AllStorageTypes", len(s3StorageTypes))
	}
	sizes := map[string]float64{}
	objects := map[string]float64{}
	seen := map[string]bool{}
	for start0 := 0; start0 < len(queries); start0 += 500 {
		end0 := start0 + 500
		if end0 > len(queries) {
			end0 = len(queries)
		}
		in := &cloudwatch.GetMetricDataInput{MetricDataQueries: queries[start0:end0], StartTime: aws.Time(start), EndTime: aws.Time(end)}
		p := cloudwatch.NewGetMetricDataPaginator(s.c.CloudWatch, in)
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(SvcCloudWatch, "GetMetricData", err, "S3 bucket sizes unknown; transfer cost estimates exclude these buckets", "")
				return
			}
			for _, r := range page.MetricDataResults {
				k, ok := ids[aws.ToString(r.Id)]
				if !ok || len(r.Values) == 0 {
					continue
				}
				// Values are newest first by default.
				seen[k.bucket] = true
				if k.metric == "BucketSizeBytes" {
					sizes[k.bucket] += r.Values[0]
				} else {
					objects[k.bucket] = r.Values[0]
				}
			}
		}
	}
	s.sink.mu.Lock()
	defer s.sink.mu.Unlock()
	for i := range s.sink.resources {
		r := &s.sink.resources[i]
		if r.Type != "AWS::S3::Bucket" || r.Region != s.region {
			continue
		}
		if !seen[r.Name] {
			if r.Attributes != nil {
				r.Attributes["size_source"] = "unavailable"
			}
			continue
		}
		r.SizeBytes = inventory.Int64(int64(sizes[r.Name]))
		r.Attributes["object_count"] = int64(objects[r.Name])
		r.Attributes["size_source"] = "cloudwatch-daily-average"
	}
}
