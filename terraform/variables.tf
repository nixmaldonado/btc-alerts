# terraform/variables.tf
variable "region" {
  description = "AWS region for all resources."
  type        = string
  default     = "us-east-1"
}

variable "project" {
  description = "Project name, used as a prefix/tag for resources."
  type        = string
  default     = "btc-alerts"
}

variable "table_name" {
  description = "DynamoDB table name for alerts and the price-state singleton."
  type        = string
  default     = "alerts"
}

variable "sender_email" {
  description = "SES-verified sender address (From) for alert emails. Sandbox mode."
  type        = string
}

variable "lambda_build_dir" {
  description = "Directory holding prebuilt Lambda bootstrap binaries (build/<name>/bootstrap)."
  type        = string
  default     = "build"
}

variable "evaluator_reserved_concurrency" {
  description = <<-EOT
    Reserved concurrent executions for the evaluator (serial tick processing).
    null = unreserved. Must stay null on accounts whose total concurrency limit
    is too low to reserve any (e.g. new accounts capped at 10, which require
    unreserved >= 10). Set to 1 once a Lambda concurrency quota increase lands.
  EOT
  type        = number
  default     = null
}
