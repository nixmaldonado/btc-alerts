# terraform/dynamodb.tf

# Provisioned (not on-demand) to honor the hard $0 always-free floor: on-demand has
# no free request tier. Always-free ceiling is 25 RCU / 25 WCU ACCOUNT-WIDE, and a
# provisioned GSI needs its own capacity, so capacity is split: base 13/13 + GSI 12/12,
# totaling 25/25. Auto-scaling is intentionally disabled.
resource "aws_dynamodb_table" "alerts" {
  name           = var.table_name
  billing_mode   = "PROVISIONED"
  read_capacity  = 13
  write_capacity = 13

  hash_key  = "PK"
  range_key = "SK"

  attribute {
    name = "PK"
    type = "S"
  }

  attribute {
    name = "SK"
    type = "S"
  }

  # Sparse GSI attributes: only armed alerts carry gsi_pk/gsi_sk.
  attribute {
    name = "gsi_pk"
    type = "S"
  }

  attribute {
    name = "gsi_sk"
    type = "S"
  }

  stream_enabled   = true
  stream_view_type = "NEW_AND_OLD_IMAGES"

  global_secondary_index {
    name            = "status-target-index"
    hash_key        = "gsi_pk"
    range_key       = "gsi_sk"
    read_capacity   = 12
    write_capacity  = 12
    projection_type = "INCLUDE"
    non_key_attributes = [
      "email",
      "direction",
    ]
  }

  point_in_time_recovery {
    enabled = false
  }
}
