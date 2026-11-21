# SAE container-service module tests (mock provider only; no cloud API is called).
# Fixture mirrors the contract's api/worker services.

mock_provider "alicloud" {
  mock_resource "alicloud_sae_load_balancer_internet" {
    defaults = {
      internet_ip = "203.0.113.10"
    }
  }
}

variables {
  project     = "contract-full"
  environment = "staging"
  prefix      = "contract-full-staging"
  region      = "eu-central-1"
  account_id  = "1234567890123456"
  vpc_id      = "vpc-mock"
  vpc_cidr    = "10.40.0.0/16"
  vswitch_ids = ["vsw-a", "vsw-b"]

  services = [
    {
      name          = "api", image = "ghcr.io/example/api:1.0.0", port = 8080, cpu = 0.5, memory_mb = 1024
      min_instances = 1, max_instances = 3, public = true, health_check_path = "/healthz", command = []
      env           = { LOG_LEVEL = "info" }, secret_env = { MAPS_API_KEY = "maps-api-key" }
      access        = { buckets = { artifacts = "read-write", images = "read" }, publish = ["telemetry"], consume = [], database = true }
    },
    {
      name          = "worker", image = "ghcr.io/example/worker:1.0.0", port = 8080, cpu = 0.25, memory_mb = 512
      min_instances = 0, max_instances = 2, public = false, health_check_path = "/healthz", command = ["/app/worker", "--queue", "ingest"]
      env           = {}, secret_env = {}
      access        = { buckets = {}, publish = [], consume = ["telemetry-ingest"], database = false }
    },
    {
      name          = "batch", image = "ghcr.io/example/batch:1.0.0", port = 9000, cpu = 1, memory_mb = 3000
      min_instances = 1, max_instances = 1, public = false, health_check_path = "/healthz", command = []
      env           = {}, secret_env = {}
      access        = { buckets = {}, publish = [], consume = [], database = false }
    },
  ]

  service_environment = {
    api    = { HERMES_SERVICE = "api", LOG_LEVEL = "info" }
    worker = { HERMES_SERVICE = "worker" }
    batch  = { HERMES_SERVICE = "batch" }
  }

  bucket_names = { artifacts = "contract-full-staging-artifacts-a1b2c3", images = "contract-full-staging-images-a1b2c3" }
  topic_names  = { telemetry = "contract-full-staging-telemetry" }
  queue_names  = { "telemetry-ingest" = "contract-full-staging-telemetry-ingest", "telemetry-archive" = "contract-full-staging-telemetry-archive" }
  secret_names = { "maps-api-key" = "contract-full-staging-maps-api-key", "webhook-signing-key" = "contract-full-staging-webhook-signing-key" }
}

run "least_privilege_policies" {
  command = apply

  variables {
    bucket_kms_key_id = "key-mock-123"
  }

  assert {
    condition     = sort(keys(output.policy_documents)) == sort(["api", "worker"])
    error_message = "A service without access must get no policy at all."
  }

  assert {
    condition = alltrue([for st in jsondecode(output.policy_documents["worker"]).Statement : (
      alltrue([for a in st.Action : !startswith(a, "oss:") && !startswith(a, "kms:") && a != "mns:PublishMessage"])
    )])
    error_message = "The worker must not read buckets, secrets or publish."
  }

  assert {
    condition = flatten([for st in jsondecode(output.policy_documents["worker"]).Statement : st.Resource]) == [
      "acs:mns:eu-central-1:1234567890123456:/queues/contract-full-staging-telemetry-ingest/messages",
    ]
    error_message = "The worker may consume only from its own subscription queue."
  }

  assert {
    condition = flatten([for st in jsondecode(output.policy_documents["api"]).Statement : st.Resource if contains(st.Action, "mns:PublishMessage")]) == [
      "acs:mns:eu-central-1:1234567890123456:/topics/contract-full-staging-telemetry/messages",
    ]
    error_message = "The api may publish only to the telemetry topic."
  }

  assert {
    condition = alltrue([for st in jsondecode(output.policy_documents["api"]).Statement : (
      !contains(st.Resource, "acs:oss:*:1234567890123456:contract-full-staging-images-a1b2c3/*") || st.Action == ["oss:GetObject"]
    )])
    error_message = "The images bucket is read-only for the api."
  }

  assert {
    condition = anytrue([for st in jsondecode(output.policy_documents["api"]).Statement : (
      contains(st.Resource, "acs:oss:*:1234567890123456:contract-full-staging-artifacts-a1b2c3/*") && contains(st.Action, "oss:PutObject")
    )])
    error_message = "The artifacts bucket is read-write for the api."
  }

  assert {
    condition = flatten([for st in jsondecode(output.policy_documents["api"]).Statement : st.Resource if contains(st.Action, "kms:GetSecretValue")]) == [
      "acs:kms:eu-central-1:1234567890123456:secret/contract-full-staging-maps-api-key",
    ]
    error_message = "The api may read only the secrets referenced by its secret_env."
  }

  assert {
    condition = anytrue([for st in jsondecode(output.policy_documents["api"]).Statement : (
      contains(st.Resource, "acs:kms:eu-central-1:1234567890123456:key/key-mock-123") && contains(st.Action, "kms:GenerateDataKey")
    )])
    error_message = "Writing to SSE-KMS buckets needs the bucket key."
  }
}

run "ingress_sizing_and_scaling" {
  command = apply

  assert {
    condition     = sort(keys(alicloud_sae_load_balancer_internet.this)) == sort(["api"]) && output.services["api"].url == "http://203.0.113.10" && output.services["worker"].url == ""
    error_message = "Only public services get an internet load balancer; private services have no URL."
  }

  assert {
    condition     = one(alicloud_sae_load_balancer_internet.this["api"].internet).protocol == "HTTP" && tostring(one(alicloud_sae_load_balancer_internet.this["api"].internet).target_port) == "8080"
    error_message = "Without a certificate a non-protected public service is served over HTTP to its container port."
  }

  assert {
    condition = (
      output.instance_sizes["worker"] == { cpu_millicores = 500, memory_mb = 1024 } &&
      output.instance_sizes["api"] == { cpu_millicores = 500, memory_mb = 1024 } &&
      output.instance_sizes["batch"] == { cpu_millicores = 1000, memory_mb = 4096 }
    )
    error_message = "CPU/memory must round up to the smallest SAE instance type that fits."
  }

  assert {
    condition     = alicloud_sae_application.this["worker"].replicas == 1 && sort(keys(alicloud_sae_application_scaling_rule.cpu)) == sort(["api", "worker"])
    error_message = "min_instances 0 becomes 1 replica; services with max > min get a CPU scaling rule."
  }

  assert {
    condition     = alicloud_sae_application.this["worker"].command == "/app/worker" && alicloud_sae_application.this["worker"].command_args_v2 == tolist(["--queue", "ingest"])
    error_message = "The spec command must map to SAE command + args."
  }

  assert {
    condition     = jsondecode(alicloud_sae_application.this["api"].envs) == [{ name = "HERMES_SERVICE", value = "api" }, { name = "LOG_LEVEL", value = "info" }]
    error_message = "SAE envs must be exactly the composition's service environment."
  }

  assert {
    condition     = alicloud_sae_application.this["api"].vswitch_id == "vsw-a,vsw-b" && length(alicloud_sae_application.this["api"].app_name) <= 36
    error_message = "Applications use every zone's vSwitch and a name within SAE's 36-character limit."
  }

  assert {
    condition     = alltrue([for r in values(alicloud_security_group_rule.vpc) : r.cidr_ip == "10.40.0.0/16"]) && sort(keys(alicloud_security_group_rule.load_balancer)) == sort(["8080"])
    error_message = "Service ports open to the VPC only, plus the load-balancer range for public ports."
  }
}

run "https_with_certificate" {
  command = apply

  variables {
    protected      = true
    certificate_id = "1234567-cn-mock"
  }

  assert {
    condition     = one(alicloud_sae_load_balancer_internet.this["api"].internet).protocol == "HTTPS" && one(alicloud_sae_load_balancer_internet.this["api"].internet).https_cert_id == "1234567-cn-mock" && output.services["api"].url == "https://203.0.113.10"
    error_message = "With a certificate the public service must be HTTPS on 443."
  }
}

run "protected_public_without_certificate_fails" {
  command = plan

  variables {
    protected = true
  }

  expect_failures = [alicloud_sae_load_balancer_internet.this]
}

run "workload_roles_trust_sae_not_account_root" {
  command = apply

  assert {
    condition     = jsondecode(output.trust_policy).Statement[0].Principal == { Service = ["sae.aliyuncs.com"] }
    error_message = "By default only the SAE service principal may assume the workload roles."
  }

  assert {
    condition     = alltrue([for k, r in alicloud_ram_role.service : !strcontains(r.assume_role_policy_document, "acs:ram::1234567890123456:root") && !strcontains(r.assume_role_policy_document, ":root")])
    error_message = "Workload role trust policies must never trust the account root."
  }
}

run "workload_roles_trust_explicit_principals" {
  command = apply

  variables {
    trusted_principals = ["acs:ram::1234567890123456:role/example-deployer"]
  }

  assert {
    condition     = alltrue([for k, r in alicloud_ram_role.service : jsondecode(r.assume_role_policy_document).Statement[0].Principal == { RAM = ["acs:ram::1234567890123456:role/example-deployer"] }])
    error_message = "trusted_principals must replace the SAE service principal on every workload role."
  }
}

run "workload_roles_reject_account_root" {
  command = plan

  variables {
    trusted_principals = ["acs:ram::1234567890123456:root"]
  }

  expect_failures = [var.trusted_principals]
}
