package awsinv

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

func ec2Tags(tags []types.Tag) map[string]string {
	return tagMap(tags, func(t types.Tag) (*string, *string) { return t.Key, t.Value })
}

func (s *scope) ec2ARN(kind, id string) string {
	if id == "" {
		return ""
	}
	return s.arn("ec2", kind+"/"+id)
}

func collectEC2(ctx context.Context, s *scope) {
	c := s.c.EC2
	const svc = SvcEC2

	// VPCs
	{
		p := ec2.NewDescribeVpcsPaginator(c, &ec2.DescribeVpcsInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeVpcs", err, "VPCs (and their CIDR ranges) were not inventoried", "")
				break
			}
			for _, v := range page.Vpcs {
				id := aws.ToString(v.VpcId)
				tags := ec2Tags(v.Tags)
				a := attrs{}.s("resource_id", id).str("cidr_block", v.CidrBlock).b("is_default", v.IsDefault).
					s("instance_tenancy", string(v.InstanceTenancy)).s("state", string(v.State))
				var extra []string
				for _, as := range v.CidrBlockAssociationSet {
					if cb := aws.ToString(as.CidrBlock); cb != "" && cb != aws.ToString(v.CidrBlock) {
						extra = append(extra, cb)
					}
				}
				a.list("secondary_cidr_blocks", extra)
				s.add(inventory.Resource{ID: s.ec2ARN("vpc", id), Type: "AWS::EC2::VPC", Name: nameTag(tags, id), Tags: tags, Attributes: a.m()})
			}
		}
	}

	// Subnets
	{
		p := ec2.NewDescribeSubnetsPaginator(c, &ec2.DescribeSubnetsInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeSubnets", err, "Subnets were not inventoried", "")
				break
			}
			for _, v := range page.Subnets {
				id := aws.ToString(v.SubnetId)
				tags := ec2Tags(v.Tags)
				arn := s.ec2ARN("subnet", id)
				a := attrs{}.s("resource_id", id).str("vpc_id", v.VpcId).str("cidr_block", v.CidrBlock).
					str("availability_zone", v.AvailabilityZone).b("map_public_ip_on_launch", v.MapPublicIpOnLaunch).
					i32("available_ip_address_count", v.AvailableIpAddressCount)
				s.add(inventory.Resource{ID: arn, Type: "AWS::EC2::Subnet", Name: nameTag(tags, id), Tags: tags, Attributes: a.m()})
				s.rel(arn, s.ec2ARN("vpc", aws.ToString(v.VpcId)), inventory.RelInVPC)
			}
		}
	}

	// Security groups with rules
	{
		p := ec2.NewDescribeSecurityGroupsPaginator(c, &ec2.DescribeSecurityGroupsInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeSecurityGroups", err, "Security groups and their rules were not inventoried", "")
				break
			}
			for _, g := range page.SecurityGroups {
				id := aws.ToString(g.GroupId)
				tags := ec2Tags(g.Tags)
				arn := s.ec2ARN("security-group", id)
				ingress, openWorld, refs := sgRules(g.IpPermissions)
				egress, _, _ := sgRules(g.IpPermissionsEgress)
				a := attrs{}.s("resource_id", id).str("group_name", g.GroupName).str("vpc_id", g.VpcId).
					str("description", g.Description).any("ingress_rules", ingress).any("egress_rules", egress).
					any("ingress_rule_count", int64(len(ingress))).any("egress_rule_count", int64(len(egress))).
					any("ingress_open_to_world", len(openWorld) > 0)
				a.list("world_open_ports", openWorld)
				s.add(inventory.Resource{ID: arn, Type: "AWS::EC2::SecurityGroup", Name: firstNonEmpty(tags["Name"], aws.ToString(g.GroupName), id), Tags: tags, Attributes: a.m()})
				s.rel(arn, s.ec2ARN("vpc", aws.ToString(g.VpcId)), inventory.RelInVPC)
				for _, ref := range refs {
					if ref != id {
						s.rel(arn, s.ec2ARN("security-group", ref), inventory.RelAllowsIngressFrom)
					}
				}
			}
		}
	}

	// Route tables
	{
		p := ec2.NewDescribeRouteTablesPaginator(c, &ec2.DescribeRouteTablesInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeRouteTables", err, "Route tables were not inventoried; routing to NAT/IGW/VPN/peering is unknown", "")
				break
			}
			for _, rt := range page.RouteTables {
				id := aws.ToString(rt.RouteTableId)
				tags := ec2Tags(rt.Tags)
				arn := s.ec2ARN("route-table", id)
				var routes []map[string]any
				main := false
				var subnets []string
				for _, as := range rt.Associations {
					if aws.ToBool(as.Main) {
						main = true
					}
					if sid := aws.ToString(as.SubnetId); sid != "" {
						subnets = append(subnets, sid)
						s.rel(arn, s.ec2ARN("subnet", sid), inventory.RelAttachedTo)
					}
				}
				for _, r := range rt.Routes {
					dest := firstNonEmpty(aws.ToString(r.DestinationCidrBlock), aws.ToString(r.DestinationIpv6CidrBlock), aws.ToString(r.DestinationPrefixListId))
					ttype, tid, tarn := routeTarget(s, r)
					routes = append(routes, map[string]any{"destination": dest, "target_type": ttype, "target_id": tid, "state": string(r.State)})
					if tarn != "" {
						s.rel(arn, tarn, inventory.RelRoutesTo)
					}
				}
				a := attrs{}.s("resource_id", id).str("vpc_id", rt.VpcId).any("main", main).any("routes", routes)
				a.list("associated_subnet_ids", subnets)
				s.add(inventory.Resource{ID: arn, Type: "AWS::EC2::RouteTable", Name: nameTag(tags, id), Tags: tags, Attributes: a.m()})
				s.rel(arn, s.ec2ARN("vpc", aws.ToString(rt.VpcId)), inventory.RelInVPC)
			}
		}
	}

	// Internet gateways
	{
		p := ec2.NewDescribeInternetGatewaysPaginator(c, &ec2.DescribeInternetGatewaysInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeInternetGateways", err, "Internet gateways were not inventoried", "")
				break
			}
			for _, g := range page.InternetGateways {
				id := aws.ToString(g.InternetGatewayId)
				tags := ec2Tags(g.Tags)
				arn := s.ec2ARN("internet-gateway", id)
				var vpcs []string
				for _, at := range g.Attachments {
					vpcs = append(vpcs, aws.ToString(at.VpcId))
					s.rel(arn, s.ec2ARN("vpc", aws.ToString(at.VpcId)), inventory.RelAttachedTo)
				}
				s.add(inventory.Resource{ID: arn, Type: "AWS::EC2::InternetGateway", Name: nameTag(tags, id), Tags: tags,
					Attributes: attrs{}.s("resource_id", id).list("attached_vpc_ids", vpcs).m()})
			}
		}
	}

	// NAT gateways
	{
		p := ec2.NewDescribeNatGatewaysPaginator(c, &ec2.DescribeNatGatewaysInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeNatGateways", err, "NAT gateways were not inventoried", "")
				break
			}
			for _, g := range page.NatGateways {
				if g.State == types.NatGatewayStateDeleted {
					continue
				}
				id := aws.ToString(g.NatGatewayId)
				tags := ec2Tags(g.Tags)
				arn := s.ec2ARN("natgateway", id)
				a := attrs{}.s("resource_id", id).str("vpc_id", g.VpcId).str("subnet_id", g.SubnetId).
					s("connectivity_type", string(g.ConnectivityType)).s("state", string(g.State))
				s.add(inventory.Resource{ID: arn, Type: "AWS::EC2::NatGateway", Name: nameTag(tags, id), Tags: tags, Attributes: a.m()})
				s.rel(arn, s.ec2ARN("vpc", aws.ToString(g.VpcId)), inventory.RelInVPC)
				s.rel(arn, s.ec2ARN("subnet", aws.ToString(g.SubnetId)), inventory.RelInSubnet)
			}
		}
	}

	// Instances
	{
		p := ec2.NewDescribeInstancesPaginator(c, &ec2.DescribeInstancesInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeInstances", err, "EC2 instances (compute workloads, relays, container hosts) were not inventoried", "")
				break
			}
			for _, res := range page.Reservations {
				for _, in := range res.Instances {
					if in.State != nil && in.State.Name == types.InstanceStateNameTerminated {
						continue
					}
					collectInstance(s, in)
				}
			}
		}
	}

	// Volumes
	{
		p := ec2.NewDescribeVolumesPaginator(c, &ec2.DescribeVolumesInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeVolumes", err, "EBS volumes (block storage sizes, encryption) were not inventoried", "")
				break
			}
			for _, v := range page.Volumes {
				id := aws.ToString(v.VolumeId)
				tags := ec2Tags(v.Tags)
				arn := s.ec2ARN("volume", id)
				var attached []string
				for _, at := range v.Attachments {
					iid := aws.ToString(at.InstanceId)
					attached = append(attached, iid)
					s.rel(arn, s.ec2ARN("instance", iid), inventory.RelAttachedTo)
				}
				a := attrs{}.s("resource_id", id).i32("size_gb", v.Size).s("volume_type", string(v.VolumeType)).
					i32("iops", v.Iops).b("encrypted", v.Encrypted).str("kms_key_id", v.KmsKeyId).
					str("availability_zone", v.AvailabilityZone).s("state", string(v.State)).s("size_basis", "provisioned")
				a.list("attached_instance_ids", attached)
				r := inventory.Resource{ID: arn, Type: "AWS::EC2::Volume", Name: nameTag(tags, id), Tags: tags, Attributes: a.m()}
				if v.Size != nil {
					r.SizeBytes = inventory.Int64(int64(*v.Size) << 30)
				}
				s.add(r)
				if k := aws.ToString(v.KmsKeyId); k != "" {
					s.rel(arn, s.kmsRef(k), inventory.RelEncryptedBy)
				}
			}
		}
	}

	// Transit gateways and attachments
	{
		p := ec2.NewDescribeTransitGatewaysPaginator(c, &ec2.DescribeTransitGatewaysInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeTransitGateways", err, "Transit gateways (hybrid/multi-VPC connectivity) were not inventoried", "")
				break
			}
			for _, t := range page.TransitGateways {
				if t.State == types.TransitGatewayStateDeleted {
					continue
				}
				id := aws.ToString(t.TransitGatewayId)
				tags := ec2Tags(t.Tags)
				arn := firstNonEmpty(aws.ToString(t.TransitGatewayArn), s.ec2ARN("transit-gateway", id))
				a := attrs{}.s("resource_id", id).s("state", string(t.State)).str("owner_id", t.OwnerId)
				if t.Options != nil {
					a.i64("amazon_side_asn", t.Options.AmazonSideAsn)
				}
				s.add(inventory.Resource{ID: arn, Type: "AWS::EC2::TransitGateway", Name: nameTag(tags, id), Tags: tags, Attributes: a.m()})
			}
		}
		pa := ec2.NewDescribeTransitGatewayAttachmentsPaginator(c, &ec2.DescribeTransitGatewayAttachmentsInput{})
		for pa.HasMorePages() {
			page, err := pa.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeTransitGatewayAttachments", err, "Transit gateway attachments (which VPCs/VPNs use a TGW) are unknown", "")
				break
			}
			for _, at := range page.TransitGatewayAttachments {
				if at.ResourceType == types.TransitGatewayAttachmentResourceTypeVpc {
					s.rel(s.ec2ARN("vpc", aws.ToString(at.ResourceId)), s.ec2ARN("transit-gateway", aws.ToString(at.TransitGatewayId)), inventory.RelAttachedTo)
				}
			}
		}
	}

	// VPN connections (tunnel pre-shared keys and the customer gateway configuration
	// document are returned by the API; Hermes never stores them).
	if out, err := c.DescribeVpnConnections(ctx, &ec2.DescribeVpnConnectionsInput{}); err != nil {
		s.gap(svc, "DescribeVpnConnections", err, "Site-to-site VPN connections were not inventoried", "")
	} else {
		for _, v := range out.VpnConnections {
			if v.State == types.VpnStateDeleted {
				continue
			}
			id := aws.ToString(v.VpnConnectionId)
			tags := ec2Tags(v.Tags)
			arn := s.ec2ARN("vpn-connection", id)
			a := attrs{}.s("resource_id", id).s("state", string(v.State)).s("type", string(v.Type)).
				str("customer_gateway_id", v.CustomerGatewayId).str("vpn_gateway_id", v.VpnGatewayId).
				str("transit_gateway_id", v.TransitGatewayId)
			if v.Options != nil {
				a.b("static_routes_only", v.Options.StaticRoutesOnly).any("tunnel_count", int64(len(v.Options.TunnelOptions)))
			}
			s.add(inventory.Resource{ID: arn, Type: "AWS::EC2::VPNConnection", Name: nameTag(tags, id), Tags: tags, Attributes: a.m()})
			if g := aws.ToString(v.VpnGatewayId); g != "" {
				s.rel(arn, s.ec2ARN("vpn-gateway", g), inventory.RelAttachedTo)
			}
			if g := aws.ToString(v.TransitGatewayId); g != "" {
				s.rel(arn, s.ec2ARN("transit-gateway", g), inventory.RelAttachedTo)
			}
		}
	}
	if out, err := c.DescribeVpnGateways(ctx, &ec2.DescribeVpnGatewaysInput{}); err != nil {
		s.gap(svc, "DescribeVpnGateways", err, "Virtual private gateways were not inventoried", "")
	} else {
		for _, g := range out.VpnGateways {
			if g.State == types.VpnStateDeleted {
				continue
			}
			id := aws.ToString(g.VpnGatewayId)
			tags := ec2Tags(g.Tags)
			arn := s.ec2ARN("vpn-gateway", id)
			var vpcs []string
			for _, at := range g.VpcAttachments {
				if at.State == types.AttachmentStatusAttached {
					vpcs = append(vpcs, aws.ToString(at.VpcId))
					s.rel(arn, s.ec2ARN("vpc", aws.ToString(at.VpcId)), inventory.RelAttachedTo)
				}
			}
			a := attrs{}.s("resource_id", id).s("state", string(g.State)).s("type", string(g.Type)).i64("amazon_side_asn", g.AmazonSideAsn)
			a.list("attached_vpc_ids", vpcs)
			s.add(inventory.Resource{ID: arn, Type: "AWS::EC2::VPNGateway", Name: nameTag(tags, id), Tags: tags, Attributes: a.m()})
		}
	}

	// VPC endpoints
	{
		p := ec2.NewDescribeVpcEndpointsPaginator(c, &ec2.DescribeVpcEndpointsInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeVpcEndpoints", err, "VPC endpoints (private access to AWS services) were not inventoried", "")
				break
			}
			for _, e := range page.VpcEndpoints {
				id := aws.ToString(e.VpcEndpointId)
				tags := ec2Tags(e.Tags)
				arn := s.ec2ARN("vpc-endpoint", id)
				a := attrs{}.s("resource_id", id).str("service_name", e.ServiceName).s("endpoint_type", string(e.VpcEndpointType)).
					str("vpc_id", e.VpcId).s("state", string(e.State)).b("private_dns_enabled", e.PrivateDnsEnabled)
				a.list("subnet_ids", e.SubnetIds).list("route_table_ids", e.RouteTableIds)
				s.add(inventory.Resource{ID: arn, Type: "AWS::EC2::VPCEndpoint", Name: nameTag(tags, firstNonEmpty(aws.ToString(e.ServiceName), id)), Tags: tags, Attributes: a.m()})
				s.rel(arn, s.ec2ARN("vpc", aws.ToString(e.VpcId)), inventory.RelInVPC)
				for _, sn := range e.SubnetIds {
					s.rel(arn, s.ec2ARN("subnet", sn), inventory.RelInSubnet)
				}
				for _, g := range e.Groups {
					s.rel(arn, s.ec2ARN("security-group", aws.ToString(g.GroupId)), inventory.RelUsesSecurityGroup)
				}
			}
		}
	}

	// VPC peering connections
	{
		p := ec2.NewDescribeVpcPeeringConnectionsPaginator(c, &ec2.DescribeVpcPeeringConnectionsInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				s.gap(svc, "DescribeVpcPeeringConnections", err, "VPC peering connections were not inventoried", "")
				break
			}
			for _, pc := range page.VpcPeeringConnections {
				id := aws.ToString(pc.VpcPeeringConnectionId)
				tags := ec2Tags(pc.Tags)
				arn := s.ec2ARN("vpc-peering-connection", id)
				a := attrs{}.s("resource_id", id)
				if pc.Status != nil {
					a.s("status", string(pc.Status.Code))
				}
				for side, info := range map[string]*types.VpcPeeringConnectionVpcInfo{"requester": pc.RequesterVpcInfo, "accepter": pc.AccepterVpcInfo} {
					if info == nil {
						continue
					}
					a.str(side+"_vpc_id", info.VpcId).str(side+"_cidr_block", info.CidrBlock).str(side+"_owner_id", info.OwnerId).str(side+"_region", info.Region)
					if aws.ToString(info.OwnerId) == s.account && aws.ToString(info.Region) == s.region {
						s.rel(arn, s.ec2ARN("vpc", aws.ToString(info.VpcId)), inventory.RelAttachedTo)
					}
				}
				s.add(inventory.Resource{ID: arn, Type: "AWS::EC2::VPCPeeringConnection", Name: nameTag(tags, id), Tags: tags, Attributes: a.m()})
			}
		}
	}
}

func routeTarget(s *scope, r types.Route) (kind, id, arn string) {
	switch {
	case aws.ToString(r.NatGatewayId) != "":
		id = aws.ToString(r.NatGatewayId)
		return "nat-gateway", id, s.ec2ARN("natgateway", id)
	case aws.ToString(r.TransitGatewayId) != "":
		id = aws.ToString(r.TransitGatewayId)
		return "transit-gateway", id, s.ec2ARN("transit-gateway", id)
	case aws.ToString(r.VpcPeeringConnectionId) != "":
		id = aws.ToString(r.VpcPeeringConnectionId)
		return "vpc-peering-connection", id, s.ec2ARN("vpc-peering-connection", id)
	case aws.ToString(r.InstanceId) != "":
		id = aws.ToString(r.InstanceId)
		return "instance", id, s.ec2ARN("instance", id)
	case aws.ToString(r.GatewayId) != "":
		id = aws.ToString(r.GatewayId)
		switch {
		case id == "local":
			return "local", id, ""
		case len(id) > 4 && id[:4] == "igw-":
			return "internet-gateway", id, s.ec2ARN("internet-gateway", id)
		case len(id) > 4 && id[:4] == "vgw-":
			return "vpn-gateway", id, s.ec2ARN("vpn-gateway", id)
		case len(id) > 5 && id[:5] == "vpce-":
			return "vpc-endpoint", id, s.ec2ARN("vpc-endpoint", id)
		}
		return "gateway", id, ""
	case aws.ToString(r.NetworkInterfaceId) != "":
		return "network-interface", aws.ToString(r.NetworkInterfaceId), ""
	}
	return "other", "", ""
}

// sgRules flattens permissions; returns the rules, world-open port descriptions and
// referenced security group ids.
func sgRules(perms []types.IpPermission) (rules []map[string]any, worldOpen []string, refs []string) {
	for _, p := range perms {
		proto := aws.ToString(p.IpProtocol)
		if proto == "-1" {
			proto = "all"
		}
		r := map[string]any{"protocol": proto}
		if p.FromPort != nil {
			r["from_port"] = int64(*p.FromPort)
		}
		if p.ToPort != nil {
			r["to_port"] = int64(*p.ToPort)
		}
		var cidrs, groups, pls []string
		world := false
		for _, rg := range p.IpRanges {
			c := aws.ToString(rg.CidrIp)
			cidrs = append(cidrs, c)
			if c == "0.0.0.0/0" {
				world = true
			}
		}
		for _, rg := range p.Ipv6Ranges {
			c := aws.ToString(rg.CidrIpv6)
			cidrs = append(cidrs, c)
			if c == "::/0" {
				world = true
			}
		}
		for _, g := range p.UserIdGroupPairs {
			gid := aws.ToString(g.GroupId)
			groups = append(groups, gid)
			refs = append(refs, gid)
		}
		for _, pl := range p.PrefixListIds {
			pls = append(pls, aws.ToString(pl.PrefixListId))
		}
		if len(cidrs) > 0 {
			r["cidrs"] = cidrs
		}
		if len(groups) > 0 {
			r["source_security_groups"] = groups
		}
		if len(pls) > 0 {
			r["prefix_lists"] = pls
		}
		if world {
			if proto == "all" {
				worldOpen = append(worldOpen, "all")
			} else {
				worldOpen = append(worldOpen, fmt.Sprintf("%s/%d-%d", proto, aws.ToInt32(p.FromPort), aws.ToInt32(p.ToPort)))
			}
		}
		rules = append(rules, r)
	}
	sort.Strings(worldOpen)
	return rules, worldOpen, refs
}

func collectInstance(s *scope, in types.Instance) {
	id := aws.ToString(in.InstanceId)
	tags := ec2Tags(in.Tags)
	arn := s.ec2ARN("instance", id)
	a := attrs{}.s("resource_id", id).s("instance_type", string(in.InstanceType)).str("image_id", in.ImageId).
		str("platform_details", in.PlatformDetails).s("architecture", string(in.Architecture)).
		str("vpc_id", in.VpcId).str("subnet_id", in.SubnetId).str("private_ip", in.PrivateIpAddress).
		any("has_public_ip", aws.ToString(in.PublicIpAddress) != "").b("ebs_optimized", in.EbsOptimized).
		s("root_device_type", string(in.RootDeviceType))
	// Catalog match key: routers, NAT instances and VPN relays disable the source/destination check.
	if in.SourceDestCheck != nil {
		a["source_dest_check"] = *in.SourceDestCheck
	} else {
		a["source_dest_check"] = true
	}
	if in.State != nil {
		a.s("state", string(in.State.Name))
	}
	if in.Placement != nil {
		a.str("availability_zone", in.Placement.AvailabilityZone)
	}
	if in.Monitoring != nil {
		a.s("monitoring", string(in.Monitoring.State))
	}
	if in.LaunchTime != nil {
		a.s("launch_time", in.LaunchTime.UTC().Format("2006-01-02T15:04:05Z"))
	}
	hints := workloadHints(tags)
	a.list("workload_hints", hints)
	for _, h := range hints {
		if h == "docker" {
			a["container_host"] = true
		}
	}
	var sgs, vols []string
	for _, g := range in.SecurityGroups {
		sgs = append(sgs, aws.ToString(g.GroupId))
		s.rel(arn, s.ec2ARN("security-group", aws.ToString(g.GroupId)), inventory.RelUsesSecurityGroup)
	}
	for _, bd := range in.BlockDeviceMappings {
		if bd.Ebs != nil {
			vols = append(vols, aws.ToString(bd.Ebs.VolumeId))
		}
	}
	a.list("security_group_ids", sgs).list("volume_ids", vols)
	if in.IamInstanceProfile != nil {
		prof := aws.ToString(in.IamInstanceProfile.Arn)
		a.s("iam_instance_profile_arn", prof)
		s.rel(arn, prof, inventory.RelAssumesRole) // resolved to the role(s) after IAM discovery
	}
	s.add(inventory.Resource{ID: arn, Type: "AWS::EC2::Instance", Name: nameTag(tags, id), Tags: tags, Attributes: a.m()})
	s.rel(arn, s.ec2ARN("vpc", aws.ToString(in.VpcId)), inventory.RelInVPC)
	s.rel(arn, s.ec2ARN("subnet", aws.ToString(in.SubnetId)), inventory.RelInSubnet)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
