package awsinv

import (
	"context"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

func ecsTags(tags []ecstypes.Tag) map[string]string {
	return tagMap(tags, func(t ecstypes.Tag) (*string, *string) { return t.Key, t.Value })
}

func collectECS(ctx context.Context, s *scope) {
	c := s.c.ECS
	const svc = SvcECS
	var clusterARNs []string
	p := ecs.NewListClustersPaginator(c, &ecs.ListClustersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListClusters", err, "ECS clusters, services and task definitions were not inventoried", "")
			return
		}
		clusterARNs = append(clusterARNs, page.ClusterArns...)
	}
	taskDefs := map[string]bool{}
	for _, batch := range chunk(clusterARNs, 100) {
		out, err := c.DescribeClusters(ctx, &ecs.DescribeClustersInput{Clusters: batch, Include: []ecstypes.ClusterField{ecstypes.ClusterFieldTags, ecstypes.ClusterFieldSettings}})
		if err != nil {
			s.gap(svc, "DescribeClusters", err, "ECS cluster details unknown", "")
			for _, arn := range batch {
				s.add(inventory.Resource{ID: arn, Type: "AWS::ECS::Cluster", Name: arn[strings.LastIndex(arn, "/")+1:], Completeness: inventory.CompletenessIdentifierOnly})
			}
			continue
		}
		for _, cl := range out.Clusters {
			a := attrs{}.str("cluster_name", cl.ClusterName).str("status", cl.Status).any("running_tasks", int64(cl.RunningTasksCount)).
				any("active_services", int64(cl.ActiveServicesCount)).list("capacity_providers", cl.CapacityProviders)
			for _, st := range cl.Settings {
				if st.Name == ecstypes.ClusterSettingNameContainerInsights {
					a.str("container_insights", st.Value)
				}
			}
			s.add(inventory.Resource{ID: aws.ToString(cl.ClusterArn), Type: "AWS::ECS::Cluster", Name: aws.ToString(cl.ClusterName), Tags: ecsTags(cl.Tags), Attributes: a.m()})
		}
	}
	for _, cluster := range clusterARNs {
		var svcARNs []string
		sp := ecs.NewListServicesPaginator(c, &ecs.ListServicesInput{Cluster: aws.String(cluster)})
		for sp.HasMorePages() {
			page, err := sp.NextPage(ctx)
			if err != nil {
				s.gap(svc, "ListServices", err, "Services of cluster "+cluster+" were not inventoried", cluster)
				break
			}
			svcARNs = append(svcARNs, page.ServiceArns...)
		}
		for _, batch := range chunk(svcARNs, 10) {
			out, err := c.DescribeServices(ctx, &ecs.DescribeServicesInput{Cluster: aws.String(cluster), Services: batch, Include: []ecstypes.ServiceField{ecstypes.ServiceFieldTags}})
			if err != nil {
				s.gap(svc, "DescribeServices", err, "ECS service details unknown", cluster)
				continue
			}
			for _, sv := range out.Services {
				arn := aws.ToString(sv.ServiceArn)
				a := attrs{}.str("service_name", sv.ServiceName).str("cluster_arn", sv.ClusterArn).
					s("cluster_name", cluster[strings.LastIndex(cluster, "/")+1:]).s("launch_type", string(sv.LaunchType)).
					any("desired_count", int64(sv.DesiredCount)).any("running_count", int64(sv.RunningCount)).
					str("task_definition", sv.TaskDefinition).s("scheduling_strategy", string(sv.SchedulingStrategy)).
					str("platform_version", sv.PlatformVersion)
				if nc := sv.NetworkConfiguration; nc != nil && nc.AwsvpcConfiguration != nil {
					a.list("subnet_ids", nc.AwsvpcConfiguration.Subnets).list("security_group_ids", nc.AwsvpcConfiguration.SecurityGroups).
						s("assign_public_ip", string(nc.AwsvpcConfiguration.AssignPublicIp))
					for _, sn := range nc.AwsvpcConfiguration.Subnets {
						s.rel(arn, s.ec2ARN("subnet", sn), inventory.RelInSubnet)
					}
					for _, g := range nc.AwsvpcConfiguration.SecurityGroups {
						s.rel(arn, s.ec2ARN("security-group", g), inventory.RelUsesSecurityGroup)
					}
				}
				var lbs []map[string]any
				for _, lb := range sv.LoadBalancers {
					m := map[string]any{"container_name": aws.ToString(lb.ContainerName)}
					if lb.ContainerPort != nil {
						m["container_port"] = int64(*lb.ContainerPort)
					}
					if tg := aws.ToString(lb.TargetGroupArn); tg != "" {
						m["target_group_arn"] = tg
						s.rel(tg, arn, inventory.RelTargets)
					}
					lbs = append(lbs, m)
				}
				if len(lbs) > 0 {
					a.any("load_balancers", lbs)
				}
				s.rel(arn, aws.ToString(sv.ClusterArn), inventory.RelDependsOn)
				if td := aws.ToString(sv.TaskDefinition); td != "" {
					s.rel(arn, td, inventory.RelDependsOn)
					taskDefs[td] = true
				}
				s.add(inventory.Resource{ID: arn, Type: "AWS::ECS::Service", Name: aws.ToString(sv.ServiceName), Tags: ecsTags(sv.Tags), Attributes: a.m()})
			}
		}
	}
	// Only the task definitions referenced by services are inventoried (not every historical revision).
	for td := range taskDefs {
		out, err := c.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{TaskDefinition: aws.String(td), Include: []ecstypes.TaskDefinitionField{ecstypes.TaskDefinitionFieldTags}})
		if err != nil {
			s.gap(svc, "DescribeTaskDefinition", err, "Task definition (images, CPU/memory, ports, secrets references) unknown", td)
			s.add(inventory.Resource{ID: td, Type: "AWS::ECS::TaskDefinition", Name: td[strings.LastIndex(td, "/")+1:], Completeness: inventory.CompletenessIdentifierOnly})
			continue
		}
		collectTaskDefinition(s, out.TaskDefinition, ecsTags(out.Tags))
	}
}

func collectTaskDefinition(s *scope, t *ecstypes.TaskDefinition, tags map[string]string) {
	arn := aws.ToString(t.TaskDefinitionArn)
	a := attrs{}.str("family", t.Family).any("revision", int64(t.Revision)).str("cpu", t.Cpu).str("memory", t.Memory).
		s("network_mode", string(t.NetworkMode)).str("task_role_arn", t.TaskRoleArn).str("execution_role_arn", t.ExecutionRoleArn)
	var compat []string
	for _, rc := range t.RequiresCompatibilities {
		compat = append(compat, string(rc))
	}
	a.list("requires_compatibilities", compat)
	var containers []map[string]any
	for _, cd := range t.ContainerDefinitions {
		m := map[string]any{"name": aws.ToString(cd.Name), "image": aws.ToString(cd.Image), "cpu": int64(cd.Cpu), "essential": cd.Essential == nil || *cd.Essential}
		if cd.Memory != nil {
			m["memory"] = int64(*cd.Memory)
		}
		if cd.MemoryReservation != nil {
			m["memory_reservation"] = int64(*cd.MemoryReservation)
		}
		var ports []map[string]any
		for _, pm := range cd.PortMappings {
			if pm.ContainerPort != nil {
				ports = append(ports, map[string]any{"container_port": int64(*pm.ContainerPort), "protocol": string(pm.Protocol)})
			}
		}
		if len(ports) > 0 {
			m["port_mappings"] = ports
		}
		// Environment variables: names only. Values are discarded here and never stored.
		var envNames []string
		for _, kv := range cd.Environment {
			envNames = append(envNames, aws.ToString(kv.Name))
		}
		if len(envNames) > 0 {
			m["environment_names"] = envNames
		}
		var secrets []map[string]any
		for _, sec := range cd.Secrets {
			ref := s.secretRef(aws.ToString(sec.ValueFrom))
			secrets = append(secrets, map[string]any{"name": aws.ToString(sec.Name), "value_from": ref})
			s.rel(arn, ref, inventory.RelUsesSecret)
		}
		if len(secrets) > 0 {
			m["secrets"] = secrets
		}
		if cd.LogConfiguration != nil {
			m["log_driver"] = string(cd.LogConfiguration.LogDriver)
			if g := cd.LogConfiguration.Options["awslogs-group"]; g != "" {
				m["log_group"] = g
				s.rel(arn, s.arn("logs", "log-group:"+g), inventory.RelWritesTo)
			}
		}
		containers = append(containers, m)
	}
	a.any("containers", containers)
	s.rel(arn, aws.ToString(t.TaskRoleArn), inventory.RelAssumesRole)
	s.rel(arn, aws.ToString(t.ExecutionRoleArn), inventory.RelAssumesRole)
	name := aws.ToString(t.Family) + ":" + strconv.Itoa(int(t.Revision))
	s.add(inventory.Resource{ID: arn, Type: "AWS::ECS::TaskDefinition", Name: name, Tags: tags, Attributes: a.m()})
}

func collectEKS(ctx context.Context, s *scope) {
	c := s.c.EKS
	const svc = SvcEKS
	p := eks.NewListClustersPaginator(c, &eks.ListClustersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListClusters", err, "EKS clusters were not inventoried", "")
			return
		}
		for _, name := range page.Clusters {
			out, err := c.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
			if err != nil {
				arn := s.arn("eks", "cluster/"+name)
				s.gap(svc, "DescribeCluster", err, "EKS cluster details unknown", arn)
				s.add(inventory.Resource{ID: arn, Type: "AWS::EKS::Cluster", Name: name, Completeness: inventory.CompletenessIdentifierOnly})
				continue
			}
			cl := out.Cluster
			arn := aws.ToString(cl.Arn)
			a := attrs{}.s("cluster_name", name).str("version", cl.Version).str("role_arn", cl.RoleArn).
				str("platform_version", cl.PlatformVersion).s("status", string(cl.Status))
			if vc := cl.ResourcesVpcConfig; vc != nil {
				a.str("vpc_id", vc.VpcId).list("subnet_ids", vc.SubnetIds).list("security_group_ids", vc.SecurityGroupIds).
					any("endpoint_public_access", vc.EndpointPublicAccess).any("endpoint_private_access", vc.EndpointPrivateAccess)
				s.rel(arn, s.ec2ARN("vpc", aws.ToString(vc.VpcId)), inventory.RelInVPC)
				for _, sn := range vc.SubnetIds {
					s.rel(arn, s.ec2ARN("subnet", sn), inventory.RelInSubnet)
				}
				for _, g := range vc.SecurityGroupIds {
					s.rel(arn, s.ec2ARN("security-group", g), inventory.RelUsesSecurityGroup)
				}
			}
			for _, ec := range cl.EncryptionConfig {
				if ec.Provider != nil {
					s.rel(arn, aws.ToString(ec.Provider.KeyArn), inventory.RelEncryptedBy)
				}
			}
			s.rel(arn, aws.ToString(cl.RoleArn), inventory.RelAssumesRole)
			s.add(inventory.Resource{ID: arn, Type: "AWS::EKS::Cluster", Name: name, Tags: cl.Tags, Attributes: a.m()})

			np := eks.NewListNodegroupsPaginator(c, &eks.ListNodegroupsInput{ClusterName: aws.String(name)})
			for np.HasMorePages() {
				ng, err := np.NextPage(ctx)
				if err != nil {
					s.gap(svc, "ListNodegroups", err, "Node groups of "+name+" unknown", arn)
					break
				}
				for _, ngName := range ng.Nodegroups {
					d, err := c.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{ClusterName: aws.String(name), NodegroupName: aws.String(ngName)})
					if err != nil {
						s.gap(svc, "DescribeNodegroup", err, "Node group details unknown", arn)
						continue
					}
					n := d.Nodegroup
					narn := aws.ToString(n.NodegroupArn)
					na := attrs{}.s("cluster_name", name).s("nodegroup_name", ngName).list("instance_types", n.InstanceTypes).
						s("ami_type", string(n.AmiType)).s("capacity_type", string(n.CapacityType)).str("node_role", n.NodeRole).
						list("subnet_ids", n.Subnets).s("status", string(n.Status))
					if sc := n.ScalingConfig; sc != nil {
						na.i32("min_size", sc.MinSize).i32("max_size", sc.MaxSize).i32("desired_size", sc.DesiredSize)
					}
					s.rel(narn, arn, inventory.RelDependsOn)
					s.rel(narn, aws.ToString(n.NodeRole), inventory.RelAssumesRole)
					s.add(inventory.Resource{ID: narn, Type: "AWS::EKS::Nodegroup", Name: ngName, Tags: n.Tags, Attributes: na.m()})
				}
			}
		}
	}
}

func collectLambda(ctx context.Context, s *scope) {
	c := s.c.Lambda
	const svc = SvcLambda
	p := lambda.NewListFunctionsPaginator(c, &lambda.ListFunctionsInput{})
	imageFunctions := 0
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListFunctions", err, "Lambda functions were not inventoried", "")
			return
		}
		for _, f := range page.Functions {
			arn := aws.ToString(f.FunctionArn)
			a := attrs{}.str("function_name", f.FunctionName).s("runtime", string(f.Runtime)).str("handler", f.Handler).
				i32("memory_mb", f.MemorySize).i32("timeout_seconds", f.Timeout).s("package_type", string(f.PackageType)).
				str("role_arn", f.Role).str("kms_key_arn", f.KMSKeyArn).str("last_modified", f.LastModified)
			var archs, layers []string
			for _, ar := range f.Architectures {
				archs = append(archs, string(ar))
			}
			for _, l := range f.Layers {
				layers = append(layers, aws.ToString(l.Arn))
			}
			a.list("architectures", archs).list("layer_arns", layers)
			if f.PackageType == "Image" {
				imageFunctions++
			}
			// Environment variables: names only; values are discarded and never stored.
			if f.Environment != nil {
				var names []string
				for k := range f.Environment.Variables {
					names = append(names, k)
				}
				a.list("environment_names", names)
			}
			if vc := f.VpcConfig; vc != nil && aws.ToString(vc.VpcId) != "" {
				a.str("vpc_id", vc.VpcId).list("subnet_ids", vc.SubnetIds).list("security_group_ids", vc.SecurityGroupIds)
				s.rel(arn, s.ec2ARN("vpc", aws.ToString(vc.VpcId)), inventory.RelInVPC)
				for _, sn := range vc.SubnetIds {
					s.rel(arn, s.ec2ARN("subnet", sn), inventory.RelInSubnet)
				}
				for _, g := range vc.SecurityGroupIds {
					s.rel(arn, s.ec2ARN("security-group", g), inventory.RelUsesSecurityGroup)
				}
			}
			if f.TracingConfig != nil {
				a.s("tracing_mode", string(f.TracingConfig.Mode))
			}
			if f.EphemeralStorage != nil {
				a.i32("ephemeral_storage_mb", f.EphemeralStorage.Size)
			}
			if f.DeadLetterConfig != nil {
				if t := aws.ToString(f.DeadLetterConfig.TargetArn); t != "" {
					a.s("dead_letter_target_arn", t)
					s.rel(arn, t, inventory.RelWritesTo)
				}
			}
			s.rel(arn, aws.ToString(f.Role), inventory.RelAssumesRole)
			if k := aws.ToString(f.KMSKeyArn); k != "" {
				s.rel(arn, k, inventory.RelEncryptedBy)
			}
			r := inventory.Resource{ID: arn, Type: "AWS::Lambda::Function", Name: aws.ToString(f.FunctionName), Attributes: a.m()}
			if tg, err := c.ListTags(ctx, &lambda.ListTagsInput{Resource: aws.String(arn)}); err != nil {
				s.gap(svc, "ListTags", err, "Tags unknown", arn)
			} else if len(tg.Tags) > 0 {
				r.Tags = tg.Tags
			}
			s.add(r)
		}
	}
	if imageFunctions > 0 {
		s.note("lambda (%s): %d container-image function(s); image URIs are not collected because lambda:GetFunction also returns a code download URL", s.region, imageFunctions)
	}
	ep := lambda.NewListEventSourceMappingsPaginator(c, &lambda.ListEventSourceMappingsInput{})
	for ep.HasMorePages() {
		page, err := ep.NextPage(ctx)
		if err != nil {
			s.gap(svc, "ListEventSourceMappings", err, "Lambda triggers (queues/streams feeding functions) unknown", "")
			return
		}
		for _, m := range page.EventSourceMappings {
			id := aws.ToString(m.UUID)
			arn := firstNonEmpty(aws.ToString(m.EventSourceMappingArn), s.arn("lambda", "event-source-mapping:"+id))
			a := attrs{}.s("uuid", id).str("event_source_arn", m.EventSourceArn).str("function_arn", m.FunctionArn).
				str("state", m.State).i32("batch_size", m.BatchSize)
			s.rel(arn, aws.ToString(m.EventSourceArn), inventory.RelReadsFrom)
			s.rel(arn, aws.ToString(m.FunctionArn), inventory.RelTargets)
			s.add(inventory.Resource{ID: arn, Type: "AWS::Lambda::EventSourceMapping", Name: id, Attributes: a.m()})
		}
	}
}

func collectECR(ctx context.Context, s *scope) {
	c := s.c.ECR
	const svc = SvcECR
	p := ecr.NewDescribeRepositoriesPaginator(c, &ecr.DescribeRepositoriesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "DescribeRepositories", err, "ECR repositories (container images) were not inventoried", "")
			return
		}
		for _, r := range page.Repositories {
			arn := aws.ToString(r.RepositoryArn)
			a := attrs{}.str("repository_name", r.RepositoryName).str("repository_uri", r.RepositoryUri).
				s("image_tag_mutability", string(r.ImageTagMutability))
			if r.ImageScanningConfiguration != nil {
				a.any("scan_on_push", r.ImageScanningConfiguration.ScanOnPush)
			}
			if ec := r.EncryptionConfiguration; ec != nil {
				a.s("encryption_type", string(ec.EncryptionType)).str("kms_key", ec.KmsKey)
				if k := aws.ToString(ec.KmsKey); k != "" {
					s.rel(arn, s.kmsRef(k), inventory.RelEncryptedBy)
				}
			}
			res := inventory.Resource{ID: arn, Type: "AWS::ECR::Repository", Name: aws.ToString(r.RepositoryName), Attributes: a.m()}
			if tg, err := c.ListTagsForResource(ctx, &ecr.ListTagsForResourceInput{ResourceArn: aws.String(arn)}); err != nil {
				s.gap(svc, "ListTagsForResource", err, "Tags unknown", arn)
			} else {
				res.Tags = tagMap(tg.Tags, func(t ecrtypes.Tag) (*string, *string) { return t.Key, t.Value })
			}
			s.add(res)
		}
	}
}

func collectELBv2(ctx context.Context, s *scope) {
	c := s.c.ELBv2
	const svc = SvcELBv2
	var arns []string
	p := elbv2.NewDescribeLoadBalancersPaginator(c, &elbv2.DescribeLoadBalancersInput{})
	var lbs []elbv2types.LoadBalancer
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			s.gap(svc, "DescribeLoadBalancers", err, "Load balancers, listeners and target groups were not inventoried", "")
			return
		}
		lbs = append(lbs, page.LoadBalancers...)
	}
	var tgs []elbv2types.TargetGroup
	tp := elbv2.NewDescribeTargetGroupsPaginator(c, &elbv2.DescribeTargetGroupsInput{})
	for tp.HasMorePages() {
		page, err := tp.NextPage(ctx)
		if err != nil {
			s.gap(svc, "DescribeTargetGroups", err, "Target groups were not inventoried", "")
			break
		}
		tgs = append(tgs, page.TargetGroups...)
	}
	for _, lb := range lbs {
		arns = append(arns, aws.ToString(lb.LoadBalancerArn))
	}
	for _, tg := range tgs {
		arns = append(arns, aws.ToString(tg.TargetGroupArn))
	}
	tags := map[string]map[string]string{}
	for _, batch := range chunk(arns, 20) {
		out, err := c.DescribeTags(ctx, &elbv2.DescribeTagsInput{ResourceArns: batch})
		if err != nil {
			s.gap(svc, "DescribeTags", err, "Load balancer tags unknown", "")
			break
		}
		for _, td := range out.TagDescriptions {
			tags[aws.ToString(td.ResourceArn)] = tagMap(td.Tags, func(t elbv2types.Tag) (*string, *string) { return t.Key, t.Value })
		}
	}
	for _, lb := range lbs {
		arn := aws.ToString(lb.LoadBalancerArn)
		a := attrs{}.s("type", string(lb.Type)).s("scheme", string(lb.Scheme)).str("dns_name", lb.DNSName).
			str("vpc_id", lb.VpcId).s("ip_address_type", string(lb.IpAddressType)).list("security_group_ids", lb.SecurityGroups)
		if lb.State != nil {
			a.s("state", string(lb.State.Code))
		}
		var subnets []string
		for _, az := range lb.AvailabilityZones {
			subnets = append(subnets, aws.ToString(az.SubnetId))
			s.rel(arn, s.ec2ARN("subnet", aws.ToString(az.SubnetId)), inventory.RelInSubnet)
		}
		a.list("subnet_ids", subnets)
		for _, g := range lb.SecurityGroups {
			s.rel(arn, s.ec2ARN("security-group", g), inventory.RelUsesSecurityGroup)
		}
		s.rel(arn, s.ec2ARN("vpc", aws.ToString(lb.VpcId)), inventory.RelInVPC)
		s.add(inventory.Resource{ID: arn, Type: "AWS::ElasticLoadBalancingV2::LoadBalancer", Name: aws.ToString(lb.LoadBalancerName), Tags: tags[arn], Attributes: a.m()})

		lp := elbv2.NewDescribeListenersPaginator(c, &elbv2.DescribeListenersInput{LoadBalancerArn: aws.String(arn)})
		for lp.HasMorePages() {
			page, err := lp.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeListeners", err, "Listeners of "+aws.ToString(lb.LoadBalancerName)+" unknown", arn)
				break
			}
			for _, l := range page.Listeners {
				larn := aws.ToString(l.ListenerArn)
				la := attrs{}.i32("port", l.Port).s("protocol", string(l.Protocol)).s("load_balancer_arn", arn)
				var certs, actions, targets []string
				for _, ct := range l.Certificates {
					certs = append(certs, aws.ToString(ct.CertificateArn))
					s.rel(larn, aws.ToString(ct.CertificateArn), inventory.RelDependsOn)
				}
				for _, act := range l.DefaultActions {
					actions = append(actions, string(act.Type))
					if tg := aws.ToString(act.TargetGroupArn); tg != "" {
						targets = append(targets, tg)
					}
					if act.ForwardConfig != nil {
						for _, t := range act.ForwardConfig.TargetGroups {
							targets = append(targets, aws.ToString(t.TargetGroupArn))
						}
					}
				}
				for _, tg := range targets {
					s.rel(larn, tg, inventory.RelTargets)
				}
				la.list("certificate_arns", certs).list("default_action_types", actions).list("target_group_arns", targets)
				s.rel(larn, arn, inventory.RelAttachedTo)
				name := aws.ToString(lb.LoadBalancerName) + ":" + strconv.Itoa(int(aws.ToInt32(l.Port)))
				s.add(inventory.Resource{ID: larn, Type: "AWS::ElasticLoadBalancingV2::Listener", Name: name, Attributes: la.m()})
			}
		}
	}
	for _, tg := range tgs {
		arn := aws.ToString(tg.TargetGroupArn)
		a := attrs{}.s("protocol", string(tg.Protocol)).i32("port", tg.Port).s("target_type", string(tg.TargetType)).
			str("vpc_id", tg.VpcId).str("health_check_path", tg.HealthCheckPath).list("load_balancer_arns", tg.LoadBalancerArns)
		s.rel(arn, s.ec2ARN("vpc", aws.ToString(tg.VpcId)), inventory.RelInVPC)
		if th, err := c.DescribeTargetHealth(ctx, &elbv2.DescribeTargetHealthInput{TargetGroupArn: aws.String(arn)}); err != nil {
			s.gap(svc, "DescribeTargetHealth", err, "Registered targets unknown", arn)
		} else {
			var ids []string
			for _, d := range th.TargetHealthDescriptions {
				if d.Target == nil {
					continue
				}
				id := aws.ToString(d.Target.Id)
				ids = append(ids, id)
				switch tg.TargetType {
				case elbv2types.TargetTypeEnumInstance:
					s.rel(arn, s.ec2ARN("instance", id), inventory.RelTargets)
				case elbv2types.TargetTypeEnumLambda:
					s.rel(arn, id, inventory.RelTargets)
				}
			}
			a.list("registered_targets", ids)
		}
		s.add(inventory.Resource{ID: arn, Type: "AWS::ElasticLoadBalancingV2::TargetGroup", Name: aws.ToString(tg.TargetGroupName), Tags: tags[arn], Attributes: a.m()})
	}
}
