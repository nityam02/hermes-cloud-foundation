package awsinv

import (
	"context"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

func collectIAM(ctx context.Context, s *scope) {
	c := s.c.IAM
	const svc = SvcIAM
	serviceLinked := 0
	p := iam.NewListRolesPaginator(c, &iam.ListRolesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListRoles", err, "IAM roles (workload identities, trust relationships) were not inventoried", "")
			break
		}
		for _, r := range page.Roles {
			if strings.HasPrefix(aws.ToString(r.Path), "/aws-service-role/") {
				serviceLinked++
				continue
			}
			arn := aws.ToString(r.Arn)
			name := aws.ToString(r.RoleName)
			ts := parseTrustPolicy(aws.ToString(r.AssumeRolePolicyDocument))
			a := attrs{}.s("role_name", name).str("path", r.Path).i32("max_session_duration", r.MaxSessionDuration).
				list("trust_services", ts.Services).list("trust_aws_principals", ts.AWS).list("trust_federated", ts.Federated).
				any("trust_wildcard_principal", ts.Wildcard).any("trust_requires_external_id", ts.ExternalIDCond)
			cross := false
			for _, pr := range ts.AWS {
				if !strings.Contains(pr, ":"+s.account+":") && pr != s.account {
					cross = true
				}
			}
			a.any("trust_cross_account", cross)
			if r.PermissionsBoundary != nil {
				a.str("permissions_boundary_arn", r.PermissionsBoundary.PermissionsBoundaryArn)
			}
			complete := true
			var attached []string
			ap := iam.NewListAttachedRolePoliciesPaginator(c, &iam.ListAttachedRolePoliciesInput{RoleName: aws.String(name)})
			for ap.HasMorePages() {
				pg, err := ap.NextPage(ctx)
				if err != nil {
					s.gap(svc, "ListAttachedRolePolicies", err, "Attached policy names unknown", arn)
					complete = false
					break
				}
				for _, pol := range pg.AttachedPolicies {
					attached = append(attached, aws.ToString(pol.PolicyName))
				}
			}
			var inline []string
			ip := iam.NewListRolePoliciesPaginator(c, &iam.ListRolePoliciesInput{RoleName: aws.String(name)})
			for ip.HasMorePages() {
				pg, err := ip.NextPage(ctx)
				if err != nil {
					s.gap(svc, "ListRolePolicies", err, "Inline policy names unknown", arn)
					complete = false
					break
				}
				inline = append(inline, pg.PolicyNames...)
			}
			if complete {
				a.any("attached_policy_names", sortedOrEmpty(attached)).any("inline_policy_names", sortedOrEmpty(inline))
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::IAM::Role", Name: name, Region: "global", Attributes: a.m()})
		}
	}
	if serviceLinked > 0 {
		s.note("iam: %d AWS service-linked role(s) were not inventoried (managed by AWS, recreated by the target services)", serviceLinked)
	}
	pp := iam.NewListInstanceProfilesPaginator(c, &iam.ListInstanceProfilesInput{})
	for pp.HasMorePages() {
		page, err := pp.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListInstanceProfiles", err, "EC2 instance profile -> role mapping unknown (instance identities partially resolved)", "")
			return
		}
		s.sink.mu.Lock()
		for _, ip := range page.InstanceProfiles {
			for _, r := range ip.Roles {
				s.sink.profiles[aws.ToString(ip.Arn)] = append(s.sink.profiles[aws.ToString(ip.Arn)], aws.ToString(r.Arn))
			}
		}
		s.sink.mu.Unlock()
	}
}

func sortedOrEmpty(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	cp := append([]string(nil), xs...)
	sort.Strings(cp)
	return cp
}

func collectSecrets(ctx context.Context, s *scope) {
	c := s.c.SecretsManager
	const svc = SvcSecretsManager
	// Metadata only. GetSecretValue is denied by the read-only guard and the IAM policy.
	p := secretsmanager.NewListSecretsPaginator(c, &secretsmanager.ListSecretsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListSecrets", err, "Secrets Manager secret names were not inventoried", "")
			return
		}
		for _, sec := range page.SecretList {
			arn := aws.ToString(sec.ARN)
			a := attrs{}.str("secret_name", sec.Name).str("kms_key_id", sec.KmsKeyId).b("rotation_enabled", sec.RotationEnabled).
				str("rotation_lambda_arn", sec.RotationLambdaARN).str("owning_service", sec.OwningService).str("primary_region", sec.PrimaryRegion)
			if sec.RotationRules != nil {
				a.i64("rotation_days", sec.RotationRules.AutomaticallyAfterDays)
			}
			if sec.LastChangedDate != nil {
				a.s("last_changed", sec.LastChangedDate.UTC().Format("2006-01-02"))
			}
			if k := aws.ToString(sec.KmsKeyId); k != "" {
				s.rel(arn, s.kmsRef(k), inventory.RelEncryptedBy)
			}
			if l := aws.ToString(sec.RotationLambdaARN); l != "" {
				s.rel(arn, l, inventory.RelDependsOn)
			}
			tags := tagMap(sec.Tags, func(t smtypes.Tag) (*string, *string) { return t.Key, t.Value })
			s.add(inventory.Resource{ID: arn, Type: "AWS::SecretsManager::Secret", Name: aws.ToString(sec.Name), Tags: tags, Attributes: a.m()})
		}
	}
}

func collectSSM(ctx context.Context, s *scope) {
	c := s.c.SSM
	const svc = SvcSSM
	// DescribeParameters returns metadata only; GetParameter* is denied by the guard.
	p := ssm.NewDescribeParametersPaginator(c, &ssm.DescribeParametersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "DescribeParameters", err, "SSM parameter names were not inventoried", "")
			return
		}
		for _, prm := range page.Parameters {
			name := aws.ToString(prm.Name)
			arn := firstNonEmpty(aws.ToString(prm.ARN), s.ssmParameterARN(name))
			a := attrs{}.s("parameter_name", name).s("type", string(prm.Type)).s("tier", string(prm.Tier)).
				str("key_id", prm.KeyId).str("data_type", prm.DataType).any("version", prm.Version)
			if k := aws.ToString(prm.KeyId); k != "" {
				s.rel(arn, s.kmsRef(k), inventory.RelEncryptedBy)
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::SSM::Parameter", Name: name, Attributes: a.m()})
		}
	}
}

func collectKMS(ctx context.Context, s *scope) {
	c := s.c.KMS
	const svc = SvcKMS
	aliases := map[string][]string{} // key id -> alias ARNs
	aliasNames := map[string][]string{}
	ap := kms.NewListAliasesPaginator(c, &kms.ListAliasesInput{})
	for ap.HasMorePages() {
		page, err := ap.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListAliases", err, "KMS alias names unknown (alias references may stay unresolved)", "")
			break
		}
		for _, al := range page.Aliases {
			if k := aws.ToString(al.TargetKeyId); k != "" {
				aliases[k] = append(aliases[k], aws.ToString(al.AliasArn))
				aliasNames[k] = append(aliasNames[k], aws.ToString(al.AliasName))
			}
		}
	}
	p := kms.NewListKeysPaginator(c, &kms.ListKeysInput{})
	awsManaged := 0
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListKeys", err, "KMS keys were not inventoried", "")
			return
		}
		for _, k := range page.Keys {
			id := aws.ToString(k.KeyId)
			arn := firstNonEmpty(aws.ToString(k.KeyArn), s.arn("kms", "key/"+id))
			d, err := c.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(id)})
			if err != nil {
				s.gap(svc, "DescribeKey", err, "Key metadata unknown", arn)
				s.add(inventory.Resource{ID: arn, Type: "AWS::KMS::Key", Name: id, Completeness: inventory.CompletenessIdentifierOnly,
					Attributes: attrs{}.s("key_id", id).list("alias_arns", aliases[id]).m()})
				continue
			}
			md := d.KeyMetadata
			if md.KeyManager == kmstypes.KeyManagerTypeAws {
				awsManaged++
				continue
			}
			if md.KeyState == kmstypes.KeyStatePendingDeletion {
				continue
			}
			a := attrs{}.s("key_id", id).s("key_state", string(md.KeyState)).s("key_usage", string(md.KeyUsage)).
				s("key_spec", string(md.KeySpec)).s("origin", string(md.Origin)).b("multi_region", md.MultiRegion).
				any("enabled", md.Enabled).s("key_manager", string(md.KeyManager)).list("alias_arns", aliases[id]).list("aliases", aliasNames[id])
			if md.KeySpec == kmstypes.KeySpecSymmetricDefault && md.Origin == kmstypes.OriginTypeAwsKms {
				if rs, err := c.GetKeyRotationStatus(ctx, &kms.GetKeyRotationStatusInput{KeyId: aws.String(id)}); err != nil {
					s.gap(svc, "GetKeyRotationStatus", err, "Key rotation status unknown", arn)
				} else {
					a.any("rotation_enabled", rs.KeyRotationEnabled)
				}
			}
			name := id
			if len(aliasNames[id]) > 0 {
				name = strings.TrimPrefix(aliasNames[id][0], "alias/")
			}
			r := inventory.Resource{ID: arn, Type: "AWS::KMS::Key", Name: name, Attributes: a.m()}
			if tg, err := c.ListResourceTags(ctx, &kms.ListResourceTagsInput{KeyId: aws.String(id)}); err != nil {
				s.gap(svc, "ListResourceTags", err, "Tags unknown", arn)
			} else {
				r.Tags = tagMap(tg.Tags, func(t kmstypes.Tag) (*string, *string) { return t.TagKey, t.TagValue })
			}
			s.add(r)
		}
	}
	if awsManaged > 0 {
		s.note("kms (%s): %d AWS-managed key(s) skipped (not migratable; target services use their own default encryption)", s.region, awsManaged)
	}
}

func collectACM(ctx context.Context, s *scope) {
	c := s.c.ACM
	const svc = SvcACM
	in := &acm.ListCertificatesInput{Includes: &acmtypes.Filters{KeyTypes: []acmtypes.KeyAlgorithm{
		acmtypes.KeyAlgorithmRsa1024, acmtypes.KeyAlgorithmRsa2048, acmtypes.KeyAlgorithmRsa3072, acmtypes.KeyAlgorithmRsa4096,
		acmtypes.KeyAlgorithmEcPrime256v1, acmtypes.KeyAlgorithmEcSecp384r1, acmtypes.KeyAlgorithmEcSecp521r1}}}
	p := acm.NewListCertificatesPaginator(c, in)
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListCertificates", err, "ACM certificates were not inventoried", "")
			return
		}
		for _, cs := range page.CertificateSummaryList {
			arn := aws.ToString(cs.CertificateArn)
			d, err := c.DescribeCertificate(ctx, &acm.DescribeCertificateInput{CertificateArn: aws.String(arn)})
			if err != nil {
				s.gap(svc, "DescribeCertificate", err, "Certificate metadata unknown", arn)
				s.add(inventory.Resource{ID: arn, Type: "AWS::CertificateManager::Certificate", Name: aws.ToString(cs.DomainName), Completeness: inventory.CompletenessIdentifierOnly})
				continue
			}
			ct := d.Certificate
			a := attrs{}.str("domain_name", ct.DomainName).s("type", string(ct.Type)).s("status", string(ct.Status)).
				s("key_algorithm", string(ct.KeyAlgorithm)).list("subject_alternative_names", ct.SubjectAlternativeNames).
				list("in_use_by", ct.InUseBy).s("renewal_eligibility", string(ct.RenewalEligibility))
			if ct.NotAfter != nil {
				a.s("not_after", ct.NotAfter.UTC().Format("2006-01-02"))
			}
			if ct.Options != nil {
				a.s("export", string(ct.Options.Export))
			}
			for _, user := range ct.InUseBy {
				s.rel(user, arn, inventory.RelDependsOn)
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::CertificateManager::Certificate", Name: aws.ToString(ct.DomainName), Attributes: a.m()})
		}
	}
}
