# Hermes read-only discovery role: the plain-HCL equivalent of
# ../cloudformation/hermes-discovery-role.json. policy.json is generated from the Hermes operation
# registry (`hermes inventory policy`); a Go test keeps it identical to the canonical file.

data "aws_iam_policy_document" "trust" {
  statement {
    sid     = "HermesDiscoveryAssumeRole"
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "AWS"
      identifiers = [var.trusted_principal_arn]
    }

    condition {
      test     = "StringEquals"
      variable = "sts:ExternalId"
      values   = [var.external_id]
    }
  }
}

resource "aws_iam_role" "discovery" {
  name                 = var.role_name
  description          = "Hermes read-only discovery (agentless, no secret values, no data reads)"
  assume_role_policy   = data.aws_iam_policy_document.trust.json
  max_session_duration = 3600

  tags = merge(var.tags, {
    "hermes-component"  = "discovery"
    "hermes-managed-by" = "hermes"
  })
}

resource "aws_iam_role_policy" "discovery" {
  name   = "HermesReadOnlyDiscovery"
  role   = aws_iam_role.discovery.id
  policy = file("${path.module}/policy.json")
}
