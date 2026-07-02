# terraform/dynamodb.tf

# On-demand (PAY_PER_REQUEST): this app's traffic is near-zero, so per-request pricing
# rounds to ~$0 — and it avoids provisioned capacity's 24/7 unit-hour clock, which billed
# on every apply/recreate cycle (partial hours round up) even while nominally at the free
# ceiling. On-demand has no free *request* tier, but at this volume that's moot.
#
# Denial-of-wallet is bounded in two layers: (1) the API Gateway usage plan gates every
# method behind an API key + quota (1000/day, 5 rps) so external floods are throttled at
# the edge; (2) the on_demand_throughput caps below are a hard DB-side ceiling — writes
# above the cap throttle (fail) rather than bill, restoring the cost ceiling that
# provisioned capacity gave us without the unit-hour clock.
resource "aws_dynamodb_table" "alerts" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"

  on_demand_throughput {
    max_read_request_units  = 100
    max_write_request_units = 100
  }

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
    name      = "status-target-index"
    hash_key  = "gsi_pk"
    range_key = "gsi_sk"

    on_demand_throughput {
      max_read_request_units  = 100
      max_write_request_units = 100
    }
    # KEYS_ONLY: the evaluator queries this index only to learn which armed alerts a
    # price move crossed, then fires each by primary key (owner+id) — which the index
    # always carries. Projecting more attributes would couple the index to the Alert
    # schema; an earlier INCLUDE list that omitted "status" silently broke every fire.
    projection_type = "KEYS_ONLY"
  }

  point_in_time_recovery {
    enabled = false
  }
}
