#!/usr/bin/env python3
"""Seed a LOCAL moto server with a hand-built (no Terraform, no CloudFormation) AWS environment.

Only ever talks to the endpoint given with --endpoint (default http://127.0.0.1:5055) and refuses
anything that is not localhost. Credentials are fake. Writes operator access keys created inside
moto (fake, local only) to --operator-env so the demo can assume the discovery role.
"""
import argparse, io, json, sys, urllib.parse, zipfile

import boto3

ACCOUNT = "123456789012"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--endpoint", default="http://127.0.0.1:5055")
    ap.add_argument("--region", default="eu-north-1")
    ap.add_argument("--policy", required=True, help="generated discovery policy JSON")
    ap.add_argument("--external-id", default="demo-external-id")
    ap.add_argument("--operator-env", required=True)
    a = ap.parse_args()
    host = urllib.parse.urlparse(a.endpoint).hostname
    if host not in ("127.0.0.1", "localhost"):
        sys.exit("refusing to seed a non-local endpoint: %s" % a.endpoint)
    s = boto3.session.Session(aws_access_key_id="testing", aws_secret_access_key="testing", region_name=a.region)
    c = lambda name: s.client(name, endpoint_url=a.endpoint)
    tags = lambda d: [{"Key": k, "Value": v} for k, v in d.items()]

    iam = c("iam")
    trust_ec2 = json.dumps({"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Principal": {"Service": "ec2.amazonaws.com"}, "Action": "sts:AssumeRole"}]})
    trust_ecs = trust_ec2.replace("ec2.amazonaws.com", "ecs-tasks.amazonaws.com")
    trust_lambda = trust_ec2.replace("ec2.amazonaws.com", "lambda.amazonaws.com")
    # Operator user (runs hermes) and the read-only discovery role it may assume with an ExternalId.
    iam.create_user(UserName="hermes-operator")
    iam.put_user_policy(UserName="hermes-operator", PolicyName="assume-discovery", PolicyDocument=json.dumps(
        {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "sts:AssumeRole", "Resource": "*"}]}))
    key = iam.create_access_key(UserName="hermes-operator")["AccessKey"]
    trust_discovery = json.dumps({"Version": "2012-10-17", "Statement": [{"Effect": "Allow",
        "Principal": {"AWS": "arn:aws:iam::%s:user/hermes-operator" % ACCOUNT}, "Action": "sts:AssumeRole",
        "Condition": {"StringEquals": {"sts:ExternalId": a.external_id}}}]})
    iam.create_role(RoleName="HermesDiscoveryReadOnly", AssumeRolePolicyDocument=trust_discovery, MaxSessionDuration=3600)
    iam.put_role_policy(RoleName="HermesDiscoveryReadOnly", PolicyName="HermesReadOnlyDiscovery", PolicyDocument=open(a.policy).read())
    # Demo only: deny two read paths so the run shows how permission gaps are recorded.
    iam.put_role_policy(RoleName="HermesDiscoveryReadOnly", PolicyName="DemoGaps", PolicyDocument=json.dumps(
        {"Version": "2012-10-17", "Statement": [{"Effect": "Deny", "Action": ["kinesis:ListStreams", "s3:GetBucketPolicy"], "Resource": "*"}]}))
    for name, trust in [("fleet-app-host", trust_ec2), ("ecs-task-api", trust_ecs), ("ecs-task-execution", trust_ecs), ("telemetry-archiver", trust_lambda)]:
        iam.create_role(RoleName=name, AssumeRolePolicyDocument=trust)
    iam.create_instance_profile(InstanceProfileName="fleet-app-host")
    iam.add_role_to_instance_profile(InstanceProfileName="fleet-app-host", RoleName="fleet-app-host")

    kms = c("kms")
    key_id = kms.create_key(Description="fleet data key", Tags=[{"TagKey": "team", "TagValue": "platform"}])["KeyMetadata"]["KeyId"]
    kms.create_alias(AliasName="alias/fleet-data", TargetKeyId=key_id)
    kms.enable_key_rotation(KeyId=key_id)

    ec2 = c("ec2")
    vpc = ec2.create_vpc(CidrBlock="10.0.0.0/16", TagSpecifications=[{"ResourceType": "vpc", "Tags": tags({"Name": "fleet-main"})}])["Vpc"]["VpcId"]
    subnets = {}
    for name, cidr, az in [("public-a", "10.0.0.0/24", "a"), ("public-b", "10.0.1.0/24", "b"), ("private-a", "10.0.10.0/24", "a"), ("private-b", "10.0.11.0/24", "b")]:
        subnets[name] = ec2.create_subnet(VpcId=vpc, CidrBlock=cidr, AvailabilityZone=a.region + az,
            TagSpecifications=[{"ResourceType": "subnet", "Tags": tags({"Name": name})}])["Subnet"]["SubnetId"]
    igw = ec2.create_internet_gateway(TagSpecifications=[{"ResourceType": "internet-gateway", "Tags": tags({"Name": "fleet-igw"})}])["InternetGateway"]["InternetGatewayId"]
    ec2.attach_internet_gateway(InternetGatewayId=igw, VpcId=vpc)
    eip = ec2.allocate_address(Domain="vpc")["AllocationId"]
    nat = ec2.create_nat_gateway(SubnetId=subnets["public-a"], AllocationId=eip, TagSpecifications=[{"ResourceType": "natgateway", "Tags": tags({"Name": "fleet-nat-a"})}])["NatGateway"]["NatGatewayId"]
    rt_pub = ec2.create_route_table(VpcId=vpc, TagSpecifications=[{"ResourceType": "route-table", "Tags": tags({"Name": "public"})}])["RouteTable"]["RouteTableId"]
    ec2.create_route(RouteTableId=rt_pub, DestinationCidrBlock="0.0.0.0/0", GatewayId=igw)
    rt_priv = ec2.create_route_table(VpcId=vpc, TagSpecifications=[{"ResourceType": "route-table", "Tags": tags({"Name": "private"})}])["RouteTable"]["RouteTableId"]
    ec2.create_route(RouteTableId=rt_priv, DestinationCidrBlock="0.0.0.0/0", NatGatewayId=nat)
    for n in ("public-a", "public-b"):
        ec2.associate_route_table(RouteTableId=rt_pub, SubnetId=subnets[n])
    for n in ("private-a", "private-b"):
        ec2.associate_route_table(RouteTableId=rt_priv, SubnetId=subnets[n])
    sg = lambda name: ec2.create_security_group(GroupName=name, Description=name, VpcId=vpc)["GroupId"]
    sg_alb, sg_app, sg_db, sg_relay = sg("alb"), sg("app"), sg("database"), sg("vpn-relay")
    ec2.authorize_security_group_ingress(GroupId=sg_alb, IpPermissions=[{"IpProtocol": "tcp", "FromPort": 443, "ToPort": 443, "IpRanges": [{"CidrIp": "0.0.0.0/0"}]}])
    ec2.authorize_security_group_ingress(GroupId=sg_app, IpPermissions=[{"IpProtocol": "tcp", "FromPort": 8080, "ToPort": 8080, "UserIdGroupPairs": [{"GroupId": sg_alb}]}])
    ec2.authorize_security_group_ingress(GroupId=sg_db, IpPermissions=[{"IpProtocol": "tcp", "FromPort": 5432, "ToPort": 5432, "UserIdGroupPairs": [{"GroupId": sg_app}]}])
    ec2.authorize_security_group_ingress(GroupId=sg_relay, IpPermissions=[{"IpProtocol": "udp", "FromPort": 51820, "ToPort": 51820, "IpRanges": [{"CidrIp": "0.0.0.0/0"}]}])
    ami = ec2.describe_images(Owners=["amazon"])["Images"][0]["ImageId"]
    workers = []
    for i, sub in ((1, "private-a"), (2, "private-b")):
        r = ec2.run_instances(ImageId=ami, MinCount=1, MaxCount=1, InstanceType="m6i.large", SubnetId=subnets[sub], SecurityGroupIds=[sg_app],
            IamInstanceProfile={"Name": "fleet-app-host"},
            TagSpecifications=[{"ResourceType": "instance", "Tags": tags({"Name": "telemetry-worker-%d" % i, "role": "docker-host", "runtime": "docker-compose", "config": "ansible"})}])
        workers.append(r["Instances"][0]["InstanceId"])
    relay = ec2.run_instances(ImageId=ami, MinCount=1, MaxCount=1, InstanceType="t3.small", SubnetId=subnets["public-a"], SecurityGroupIds=[sg_relay],
        TagSpecifications=[{"ResourceType": "instance", "Tags": tags({"Name": "vpn-relay", "role": "wireguard-relay", "purpose": "device-connectivity"})}])["Instances"][0]["InstanceId"]
    ec2.modify_instance_attribute(InstanceId=relay, SourceDestCheck={"Value": False})

    rds = c("rds")
    rds.create_db_subnet_group(DBSubnetGroupName="fleet-db-subnets", DBSubnetGroupDescription="fleet", SubnetIds=[subnets["private-a"], subnets["private-b"]])
    rds.create_db_parameter_group(DBParameterGroupName="fleet-pg15-postgis", DBParameterGroupFamily="postgres15", Description="PostGIS-enabled parameters")
    rds.create_db_instance(DBInstanceIdentifier="fleet-db", Engine="postgres", EngineVersion="15.5", DBInstanceClass="db.m6g.large", MultiAZ=True,
        AllocatedStorage=200, StorageEncrypted=True, KmsKeyId=key_id, DBSubnetGroupName="fleet-db-subnets", VpcSecurityGroupIds=[sg_db],
        DBParameterGroupName="fleet-pg15-postgis", BackupRetentionPeriod=7, MasterUsername="fleetadmin", MasterUserPassword="demo-not-a-secret",
        Tags=tags({"team": "fleet"}))

    s3 = c("s3")
    for name, objs in (("fleet-flight-logs-example", 5), ("fleet-mission-images-example", 3), ("fleet-static-site-example", 2)):
        s3.create_bucket(Bucket=name, CreateBucketConfiguration={"LocationConstraint": a.region})
        s3.put_bucket_versioning(Bucket=name, VersioningConfiguration={"Status": "Enabled"})
        s3.put_public_access_block(Bucket=name, PublicAccessBlockConfiguration={"BlockPublicAcls": True, "IgnorePublicAcls": True, "BlockPublicPolicy": True, "RestrictPublicBuckets": True})
        s3.put_bucket_encryption(Bucket=name, ServerSideEncryptionConfiguration={"Rules": [{"ApplyServerSideEncryptionByDefault": {"SSEAlgorithm": "aws:kms", "KMSMasterKeyID": key_id}}]})
        for i in range(objs):
            s3.put_object(Bucket=name, Key="sample/%03d.json" % i, Body=b'{"demo": true}')
    s3.put_bucket_tagging(Bucket="fleet-flight-logs-example", Tagging={"TagSet": tags({"team": "fleet"})})

    sqs, sns = c("sqs"), c("sns")
    dlq_url = sqs.create_queue(QueueName="telemetry-ingest-dlq")["QueueUrl"]
    dlq_arn = sqs.get_queue_attributes(QueueUrl=dlq_url, AttributeNames=["QueueArn"])["Attributes"]["QueueArn"]
    ingest = sqs.create_queue(QueueName="telemetry-ingest", Attributes={"VisibilityTimeout": "60", "RedrivePolicy": json.dumps({"deadLetterTargetArn": dlq_arn, "maxReceiveCount": "5"})})["QueueUrl"]
    archive = sqs.create_queue(QueueName="telemetry-archive", Attributes={"VisibilityTimeout": "120", "MessageRetentionPeriod": "1209600"})["QueueUrl"]
    sqs.create_queue(QueueName="dispatch-commands.fifo", Attributes={"FifoQueue": "true", "ContentBasedDeduplication": "true"})
    topic = sns.create_topic(Name="telemetry", Tags=[{"Key": "team", "Value": "fleet"}])["TopicArn"]
    for q in (ingest, archive):
        arn = sqs.get_queue_attributes(QueueUrl=q, AttributeNames=["QueueArn"])["Attributes"]["QueueArn"]
        sns.subscribe(TopicArn=topic, Protocol="sqs", Endpoint=arn, Attributes={"RawMessageDelivery": "true"})

    sm = c("secretsmanager")
    secret_arn = sm.create_secret(Name="fleet/db-password", SecretString="demo-not-a-secret", KmsKeyId=key_id)["ARN"]
    sm.create_secret(Name="fleet/maps-api-key", SecretString="demo-not-a-secret")

    c("dynamodb").create_table(TableName="device-state", BillingMode="PAY_PER_REQUEST",
        AttributeDefinitions=[{"AttributeName": "device_id", "AttributeType": "S"}], KeySchema=[{"AttributeName": "device_id", "KeyType": "HASH"}])

    logs = c("logs")
    logs.create_log_group(logGroupName="/ecs/fleet-api")
    logs.put_retention_policy(logGroupName="/ecs/fleet-api", retentionInDays=30)

    ecs = c("ecs")
    ecs.create_cluster(clusterName="fleet")
    td = ecs.register_task_definition(family="fleet-api", networkMode="awsvpc", requiresCompatibilities=["FARGATE"], cpu="512", memory="1024",
        taskRoleArn="arn:aws:iam::%s:role/ecs-task-api" % ACCOUNT, executionRoleArn="arn:aws:iam::%s:role/ecs-task-execution" % ACCOUNT,
        containerDefinitions=[{"name": "api", "image": "ghcr.io/example/fleet-api:2.8.1", "essential": True, "portMappings": [{"containerPort": 8080}],
            "environment": [{"name": "LOG_LEVEL", "value": "demo-env-value"}], "secrets": [{"name": "DATABASE_PASSWORD", "valueFrom": secret_arn}],
            "logConfiguration": {"logDriver": "awslogs", "options": {"awslogs-group": "/ecs/fleet-api", "awslogs-region": a.region}}}])["taskDefinition"]["taskDefinitionArn"]

    elb = c("elbv2")
    lb = elb.create_load_balancer(Name="fleet-public", Subnets=[subnets["public-a"], subnets["public-b"]], SecurityGroups=[sg_alb], Scheme="internet-facing", Type="application")["LoadBalancers"][0]["LoadBalancerArn"]
    tg = elb.create_target_group(Name="fleet-api", Protocol="HTTP", Port=8080, VpcId=vpc, TargetType="ip", HealthCheckPath="/healthz")["TargetGroups"][0]["TargetGroupArn"]
    elb.create_listener(LoadBalancerArn=lb, Protocol="HTTP", Port=80, DefaultActions=[{"Type": "forward", "TargetGroupArn": tg}])
    ecs.create_service(cluster="fleet", serviceName="fleet-api", taskDefinition=td, desiredCount=2, launchType="FARGATE",
        loadBalancers=[{"targetGroupArn": tg, "containerName": "api", "containerPort": 8080}],
        networkConfiguration={"awsvpcConfiguration": {"subnets": [subnets["private-a"], subnets["private-b"]], "securityGroups": [sg_app], "assignPublicIp": "DISABLED"}})

    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w") as z:
        z.writestr("app.py", "def handler(event, context):\n    return {'ok': True}\n")
    lam = c("lambda")
    fn = lam.create_function(FunctionName="telemetry-archiver", Runtime="python3.12", Role="arn:aws:iam::%s:role/telemetry-archiver" % ACCOUNT,
        Handler="app.handler", Code={"ZipFile": buf.getvalue()}, MemorySize=512, Timeout=60,
        Environment={"Variables": {"ARCHIVE_BUCKET": "fleet-flight-logs-example", "LOG_LEVEL": "demo-env-value"}})["FunctionArn"]
    lam.create_event_source_mapping(EventSourceArn=sqs.get_queue_attributes(QueueUrl=archive, AttributeNames=["QueueArn"])["Attributes"]["QueueArn"], FunctionName=fn, BatchSize=10)

    with open(a.operator_env, "w") as f:
        f.write("AWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\n" % (key["AccessKeyId"], key["SecretAccessKey"]))
    print("seeded: vpc %s, %d instances (relay %s), rds fleet-db, 3 buckets, topic + 4 queues, 2 secrets, ECS service, ALB, Lambda" % (vpc, len(workers) + 1, relay))


if __name__ == "__main__":
    main()
