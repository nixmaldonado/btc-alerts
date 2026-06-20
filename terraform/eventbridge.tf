# terraform/eventbridge.tf

resource "aws_cloudwatch_event_rule" "tick" {
  name                = "${var.project}-tick"
  description         = "Fires the evaluator once per minute to poll BTC price."
  schedule_expression = "rate(1 minute)"
}

resource "aws_cloudwatch_event_target" "evaluator" {
  rule      = aws_cloudwatch_event_rule.tick.name
  target_id = "evaluator"
  arn       = aws_lambda_function.evaluator.arn
}

resource "aws_lambda_permission" "allow_eventbridge" {
  statement_id  = "AllowExecutionFromEventBridge"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.evaluator.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.tick.arn
}

# EventBridge invokes the evaluator asynchronously, where a failed tick would
# otherwise be retried (up to 2x) from Lambda's internal queue and could overlap
# a later scheduled tick. Disabling retries — together with the 30s timeout (well
# under the 60s tick interval) and the conditional ARMED->FIRED write in FireAlert
# — keeps ticks effectively single-flight without reserving concurrency.
resource "aws_lambda_function_event_invoke_config" "evaluator" {
  function_name                = aws_lambda_function.evaluator.function_name
  maximum_retry_attempts       = 0
  maximum_event_age_in_seconds = 60
}
