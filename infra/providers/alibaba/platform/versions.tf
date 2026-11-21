terraform {
  required_version = ">= 1.10"

  required_providers {
    alicloud = {
      source  = "aliyun/alicloud"
      version = "~> 2.2"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.9"
    }
  }
}
