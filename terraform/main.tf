terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "5.54.1"
    }
  }
}

resource "aws_dynamodb_table" "alerts" {
  name      = "alerts"
  billing_mode = "ON-DEMAND"
}