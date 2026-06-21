# terraform/streams.tf

resource "aws_sqs_queue" "notifier_dlq" {
  name                      = "${var.project}-notifier-dlq"
  message_retention_seconds = 1209600 # 14 days
}

resource "aws_lambda_event_source_mapping" "notifier_stream" {
  event_source_arn  = aws_dynamodb_table.alerts.stream_arn
  function_name     = aws_lambda_function.notifier.arn
  starting_position = "LATEST"

  # Only alert items (PK begins with OWNER#, SK begins with ALERT#) wake the Notifier.
  # The SK prefix excludes the per-owner PROFILE item (email edits) as well as
  # STATE#PRICE writes, so neither once-a-minute price churn nor profile updates churn
  # the Notifier.
  filter_criteria {
    filter {
      pattern = jsonencode({
        dynamodb = {
          Keys = {
            PK = {
              S = [{ prefix = "OWNER#" }]
            }
            SK = {
              S = [{ prefix = "ALERT#" }]
            }
          }
        }
      })
    }
  }

  function_response_types = ["ReportBatchItemFailures"]

  destination_config {
    on_failure {
      destination_arn = aws_sqs_queue.notifier_dlq.arn
    }
  }
}
