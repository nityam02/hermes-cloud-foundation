terraform {
  required_version = ">= 1.10"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 8.12"
    }
    # Only for google_project_service_identity (Cloud SQL and Secret Manager
    # service agents needed for CMEK); that resource is beta-only in v8.
    google-beta = {
      source  = "hashicorp/google-beta"
      version = "~> 8.12"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.9"
    }
  }
}
