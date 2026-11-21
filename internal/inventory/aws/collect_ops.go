package awsinv

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/route53"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

func collectRoute53(ctx context.Context, s *scope) {
	c := s.c.Route53
	const svc = SvcRoute53
	p := route53.NewListHostedZonesPaginator(c, &route53.ListHostedZonesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListHostedZones", err, "Route 53 hosted zones were not inventoried", "")
			return
		}
		for _, z := range page.HostedZones {
			zoneID := strings.TrimPrefix(aws.ToString(z.Id), "/hostedzone/")
			arn := fmt.Sprintf("arn:%s:route53:::hostedzone/%s", s.partition, zoneID)
			name := strings.TrimSuffix(aws.ToString(z.Name), ".")
			// Record counts only; record values are not collected.
			a := attrs{}.s("zone_id", zoneID).s("domain_name", name).i64("record_count", z.ResourceRecordSetCount)
			private := false
			if z.Config != nil {
				private = z.Config.PrivateZone
				a.str("comment", z.Config.Comment)
			}
			a.any("private_zone", private)
			if private {
				out, err := c.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: z.Id})
				if err != nil {
					s.gap(svc, "GetHostedZone", err, "VPC associations of a private zone unknown", arn)
				} else {
					var vpcs []string
					for _, v := range out.VPCs {
						vpcs = append(vpcs, aws.ToString(v.VPCId))
						vs := &scope{region: string(v.VPCRegion), account: s.account, partition: s.partition}
						s.rel(arn, vs.ec2ARN("vpc", aws.ToString(v.VPCId)), inventory.RelInVPC)
					}
					a.list("vpc_ids", vpcs)
				}
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::Route53::HostedZone", Name: name, Region: "global", Attributes: a.m()})
		}
	}
}

var s3OriginRe = regexp.MustCompile(`^([a-z0-9][a-z0-9.-]{1,61}[a-z0-9])\.s3[.-]([a-z0-9-]+\.)?amazonaws\.com$`)

func collectCloudFront(ctx context.Context, s *scope) {
	const svc = SvcCloudFront
	p := cloudfront.NewListDistributionsPaginator(s.c.CloudFront, &cloudfront.ListDistributionsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListDistributions", err, "CloudFront distributions were not inventoried", "")
			return
		}
		if page.DistributionList == nil {
			continue
		}
		for _, d := range page.DistributionList.Items {
			arn := aws.ToString(d.ARN)
			a := attrs{}.str("distribution_id", d.Id).str("domain_name", d.DomainName).b("enabled", d.Enabled).
				s("price_class", string(d.PriceClass)).str("web_acl_id", d.WebACLId).s("http_version", string(d.HttpVersion)).str("status", d.Status)
			if d.Aliases != nil {
				a.list("aliases", d.Aliases.Items)
			}
			var origins []map[string]any
			if d.Origins != nil {
				for _, o := range d.Origins.Items {
					dom := strings.ToLower(aws.ToString(o.DomainName))
					kind := "custom"
					if m := s3OriginRe.FindStringSubmatch(dom); m != nil {
						kind = "s3"
						s.rel(arn, fmt.Sprintf("arn:%s:s3:::%s", s.partition, m[1]), inventory.RelReadsFrom)
					} else {
						s.rel(arn, "dns:"+dom, inventory.RelReadsFrom) // resolved to a load balancer when known
					}
					origins = append(origins, map[string]any{"id": aws.ToString(o.Id), "domain_name": dom, "type": kind})
				}
			}
			a.any("origins", origins)
			if vc := d.ViewerCertificate; vc != nil {
				if cert := aws.ToString(vc.ACMCertificateArn); cert != "" {
					a.s("acm_certificate_arn", cert)
					s.rel(arn, cert, inventory.RelDependsOn)
				}
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::CloudFront::Distribution", Name: firstNonEmpty(aws.ToString(d.DomainName), aws.ToString(d.Id)), Region: "global", Attributes: a.m()})
		}
	}
}

func collectAPIGatewayV2(ctx context.Context, s *scope) {
	c := s.c.APIGatewayV2
	const svc = SvcAPIGatewayV2
	var token *string
	for {
		out, err := c.GetApis(ctx, &apigatewayv2.GetApisInput{NextToken: token})
		if err != nil {
			s.gap(svc, "GetApis", err, "API Gateway HTTP/WebSocket APIs were not inventoried", "")
			return
		}
		for _, api := range out.Items {
			id := aws.ToString(api.ApiId)
			arn := fmt.Sprintf("arn:%s:apigateway:%s::/apis/%s", s.partition, s.region, id)
			a := attrs{}.s("api_id", id).s("protocol_type", string(api.ProtocolType)).str("api_endpoint", api.ApiEndpoint).
				b("disable_execute_api_endpoint", api.DisableExecuteApiEndpoint)
			if ig, err := c.GetIntegrations(ctx, &apigatewayv2.GetIntegrationsInput{ApiId: aws.String(id)}); err != nil {
				s.gap(svc, "GetIntegrations", err, "API integrations (backend targets) unknown", arn)
			} else {
				var types []string
				for _, it := range ig.Items {
					types = append(types, string(it.IntegrationType))
					uri := aws.ToString(it.IntegrationUri)
					if strings.HasPrefix(uri, "arn:") {
						s.rel(arn, uri, inventory.RelTargets)
					}
				}
				a.list("integration_types", types)
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::ApiGatewayV2::Api", Name: aws.ToString(api.Name), Tags: api.Tags, Attributes: a.m()})
		}
		if aws.ToString(out.NextToken) == "" {
			return
		}
		token = out.NextToken
	}
}

func collectLogs(ctx context.Context, s *scope) {
	const svc = SvcLogs
	p := cloudwatchlogs.NewDescribeLogGroupsPaginator(s.c.Logs, &cloudwatchlogs.DescribeLogGroupsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "DescribeLogGroups", err, "CloudWatch log groups (retention, encryption) were not inventoried", "")
			return
		}
		for _, g := range page.LogGroups {
			name := aws.ToString(g.LogGroupName)
			arn := strings.TrimSuffix(aws.ToString(g.Arn), ":*")
			if arn == "" {
				arn = s.arn("logs", "log-group:"+name)
			}
			a := attrs{}.s("log_group_name", name).i32("retention_days", g.RetentionInDays).i64("stored_bytes", g.StoredBytes).
				str("kms_key_id", g.KmsKeyId).s("log_group_class", string(g.LogGroupClass)).i32("metric_filter_count", g.MetricFilterCount)
			if k := aws.ToString(g.KmsKeyId); k != "" {
				s.rel(arn, s.kmsRef(k), inventory.RelEncryptedBy)
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::Logs::LogGroup", Name: name, Attributes: a.m()})
		}
	}
}

func collectAlarms(ctx context.Context, s *scope) {
	const svc = SvcCloudWatch
	p := cloudwatch.NewDescribeAlarmsPaginator(s.c.CloudWatch, &cloudwatch.DescribeAlarmsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "DescribeAlarms", err, "CloudWatch alarms were not inventoried", "")
			return
		}
		for _, al := range page.MetricAlarms {
			arn := aws.ToString(al.AlarmArn)
			var dims []string
			for _, d := range al.Dimensions {
				dims = append(dims, aws.ToString(d.Name)+"="+aws.ToString(d.Value))
			}
			a := attrs{}.str("alarm_name", al.AlarmName).str("namespace", al.Namespace).str("metric_name", al.MetricName).
				s("comparison_operator", string(al.ComparisonOperator)).list("dimensions", dims).list("alarm_actions", al.AlarmActions)
			if al.Threshold != nil {
				a.any("threshold", *al.Threshold)
			}
			for _, act := range al.AlarmActions {
				if strings.Contains(act, ":sns:") {
					s.rel(arn, act, inventory.RelPublishesTo)
				}
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::CloudWatch::Alarm", Name: aws.ToString(al.AlarmName), Attributes: a.m()})
		}
		for _, al := range page.CompositeAlarms {
			arn := aws.ToString(al.AlarmArn)
			a := attrs{}.str("alarm_name", al.AlarmName).any("composite", true).list("alarm_actions", al.AlarmActions)
			for _, act := range al.AlarmActions {
				if strings.Contains(act, ":sns:") {
					s.rel(arn, act, inventory.RelPublishesTo)
				}
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::CloudWatch::CompositeAlarm", Name: aws.ToString(al.AlarmName), Attributes: a.m()})
		}
	}
}

// collectCloudFormation reads stacks only to annotate managed_by; stacks are not inventory
// resources and the inventory never depends on them.
func collectCloudFormation(ctx context.Context, s *scope) {
	c := s.c.CloudFormation
	const svc = SvcCloudFormation
	p := cloudformation.NewDescribeStacksPaginator(c, &cloudformation.DescribeStacksInput{})
	var stacks []string
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "DescribeStacks", err, "managed_by=cloudformation is only derived from tags (stack membership unknown)", "")
			return
		}
		for _, st := range page.Stacks {
			if st.StackStatus == cfntypes.StackStatusDeleteComplete {
				continue
			}
			stacks = append(stacks, aws.ToString(st.StackName))
		}
	}
	var res []inventory.StackResource
	for _, name := range stacks {
		rp := cloudformation.NewListStackResourcesPaginator(c, &cloudformation.ListStackResourcesInput{StackName: aws.String(name)})
		for rp.HasMorePages() {
			page, err := rp.NextPage(ctx)
			if err != nil {
				s.gap(svc, "ListStackResources", err, "Resources of stack "+name+" not annotated", "")
				break
			}
			for _, r := range page.StackResourceSummaries {
				res = append(res, inventory.StackResource{Stack: name, PhysicalID: aws.ToString(r.PhysicalResourceId), Type: aws.ToString(r.ResourceType)})
			}
		}
	}
	if len(stacks) > 0 {
		s.note("cloudformation (%s): %d stack(s) used only to annotate managed_by", s.region, len(stacks))
	}
	s.sink.mu.Lock()
	s.sink.stackRes = append(s.sink.stackRes, res...)
	s.sink.mu.Unlock()
}

// collectCloudControl is the optional generic fallback for resource types without a
// dedicated collector (--cloudcontrol-types). It records identifier, type, region and tags
// only (completeness identifier-only) and never stores properties: a Cloud Control model can
// carry secret VALUES under keys no name filter recognises (ECS and Lambda environment values,
// launch-template UserData, SSM parameter values, connection strings), and a key-name
// sanitizer cannot tell them apart (F-06). GetResource (the full model) is therefore not
// called; the ListResources properties are parsed in memory for the ARN and the tags only.
func collectCloudControl(ctx context.Context, s *scope) {
	c := s.c.CloudControl
	const svc = SvcCloudControl
	for _, typ := range s.ccTypes {
		p := cloudcontrol.NewListResourcesPaginator(c, &cloudcontrol.ListResourcesInput{TypeName: aws.String(typ)})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "ListResources", err, typ+" resources were not inventoried", "")
				break
			}
			for _, rd := range page.ResourceDescriptions {
				s.add(cloudControlResource(s, typ, aws.ToString(rd.Identifier), aws.ToString(rd.Properties)))
			}
		}
	}
}

// cloudControlResource keeps the identifier (the ARN as the resource id when the model has
// one), type, region and tags. Every other property is discarded.
func cloudControlResource(s *scope, typ, ident, props string) inventory.Resource {
	var m map[string]any
	_ = json.Unmarshal([]byte(props), &m)
	id := typ + "|" + s.region + "|" + ident
	if arn, ok := m["Arn"].(string); ok && strings.HasPrefix(arn, "arn:") {
		id = arn
	}
	return inventory.Resource{ID: id, Type: typ, Name: ident, Region: s.region, Tags: cloudControlTags(m["Tags"]),
		Completeness: inventory.CompletenessIdentifierOnly, Attributes: map[string]any{"cloudcontrol_identifier": ident}}
}

// cloudControlTags reads tags in either Cloud Control shape: [{Key, Value}] or {key: value}.
func cloudControlTags(v any) map[string]string {
	tags := map[string]string{}
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			if tm, ok := x.(map[string]any); ok {
				k, _ := tm["Key"].(string)
				val, _ := tm["Value"].(string)
				if k != "" {
					tags[k] = val
				}
			}
		}
	case map[string]any:
		for k, x := range t {
			if val, ok := x.(string); ok {
				tags[k] = val
			}
		}
	}
	if len(tags) == 0 {
		return nil
	}
	return tags
}
