output "buckets" {
  description = "Spec bucket name -> {name, url}."
  value = {
    for k, b in alicloud_oss_bucket.this : k => {
      name = b.bucket
      url  = "oss://${b.bucket}"
    }
  }
}

output "tls_only_policies" {
  description = "Spec bucket name -> bucket policy JSON (denies requests without TLS)."
  value       = local.tls_only_policies
}
