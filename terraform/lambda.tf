# terraform/lambda.tf

# Each Lambda is a prebuilt Go binary named `bootstrap` (provided.al2023, arm64).
# Build before apply:
#   GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o build/<name>/bootstrap ./cmd/<name>

data "archive_file" "api" {
  type        = "zip"
  source_file = "${var.lambda_build_dir}/api/bootstrap"
  output_path = "${var.lambda_build_dir}/api.zip"
}

data "archive_file" "evaluator" {
  type        = "zip"
  source_file = "${var.lambda_build_dir}/evaluator/bootstrap"
  output_path = "${var.lambda_build_dir}/evaluator.zip"
}

data "archive_file" "notifier" {
  type        = "zip"
  source_file = "${var.lambda_build_dir}/notifier/bootstrap"
  output_path = "${var.lambda_build_dir}/notifier.zip"
}

resource "aws_lambda_function" "api" {
  function_name = "${var.project}-api"
  role          = aws_iam_role.api.arn
  runtime       = "provided.al2023"
  handler       = "bootstrap"
  architectures = ["arm64"]

  filename         = data.archive_file.api.output_path
  source_code_hash = data.archive_file.api.output_base64sha256

  timeout     = 10
  memory_size = 128

  environment {
    variables = {
      ALERTS_TABLE = aws_dynamodb_table.alerts.name
    }
  }
}

resource "aws_lambda_function" "evaluator" {
  function_name = "${var.project}-evaluator"
  role          = aws_iam_role.evaluator.arn
  runtime       = "provided.al2023"
  handler       = "bootstrap"
  architectures = ["arm64"]

  filename         = data.archive_file.evaluator.output_path
  source_code_hash = data.archive_file.evaluator.output_base64sha256

  timeout     = 30
  memory_size = 128

  # Serial tick processing keeps crossing detection ordered without a queue.
  # Configurable because reserving any concurrency requires the account's total
  # limit to stay >= 10 unreserved; new accounts capped at 10 must leave this null
  # until a Lambda concurrency quota increase lands, then set it back to 1.
  reserved_concurrent_executions = var.evaluator_reserved_concurrency

  environment {
    variables = {
      ALERTS_TABLE       = aws_dynamodb_table.alerts.name
      COINGECKO_BASE_URL = "https://api.coingecko.com/api/v3"
    }
  }
}

resource "aws_lambda_function" "notifier" {
  function_name = "${var.project}-notifier"
  role          = aws_iam_role.notifier.arn
  runtime       = "provided.al2023"
  handler       = "bootstrap"
  architectures = ["arm64"]

  filename         = data.archive_file.notifier.output_path
  source_code_hash = data.archive_file.notifier.output_base64sha256

  timeout     = 15
  memory_size = 128

  environment {
    variables = {
      ALERTS_TABLE = aws_dynamodb_table.alerts.name
      SENDER_EMAIL = var.sender_email
    }
  }
}
