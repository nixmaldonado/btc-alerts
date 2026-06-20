# terraform/apigateway.tf

resource "aws_api_gateway_rest_api" "alerts" {
  name        = "${var.project}-api"
  description = "Price Alert API for btc-alerts."
}

# ---- Resources: /alerts, /alerts/{id}, /alerts/{id}/rearm ----
resource "aws_api_gateway_resource" "alerts" {
  rest_api_id = aws_api_gateway_rest_api.alerts.id
  parent_id   = aws_api_gateway_rest_api.alerts.root_resource_id
  path_part   = "alerts"
}

resource "aws_api_gateway_resource" "alert_id" {
  rest_api_id = aws_api_gateway_rest_api.alerts.id
  parent_id   = aws_api_gateway_resource.alerts.id
  path_part   = "{id}"
}

resource "aws_api_gateway_resource" "rearm" {
  rest_api_id = aws_api_gateway_rest_api.alerts.id
  parent_id   = aws_api_gateway_resource.alert_id.id
  path_part   = "rearm"
}

# ---- Methods (all require an API key) ----
resource "aws_api_gateway_method" "post_alerts" {
  rest_api_id      = aws_api_gateway_rest_api.alerts.id
  resource_id      = aws_api_gateway_resource.alerts.id
  http_method      = "POST"
  authorization    = "NONE"
  api_key_required = true
}

resource "aws_api_gateway_method" "get_alerts" {
  rest_api_id      = aws_api_gateway_rest_api.alerts.id
  resource_id      = aws_api_gateway_resource.alerts.id
  http_method      = "GET"
  authorization    = "NONE"
  api_key_required = true
}

resource "aws_api_gateway_method" "delete_alert" {
  rest_api_id      = aws_api_gateway_rest_api.alerts.id
  resource_id      = aws_api_gateway_resource.alert_id.id
  http_method      = "DELETE"
  authorization    = "NONE"
  api_key_required = true
}

resource "aws_api_gateway_method" "post_rearm" {
  rest_api_id      = aws_api_gateway_rest_api.alerts.id
  resource_id      = aws_api_gateway_resource.rearm.id
  http_method      = "POST"
  authorization    = "NONE"
  api_key_required = true
}

# ---- AWS_PROXY integrations to the API Lambda ----
resource "aws_api_gateway_integration" "post_alerts" {
  rest_api_id             = aws_api_gateway_rest_api.alerts.id
  resource_id             = aws_api_gateway_resource.alerts.id
  http_method             = aws_api_gateway_method.post_alerts.http_method
  integration_http_method = "POST"
  type                    = "AWS_PROXY"
  uri                     = aws_lambda_function.api.invoke_arn
}

resource "aws_api_gateway_integration" "get_alerts" {
  rest_api_id             = aws_api_gateway_rest_api.alerts.id
  resource_id             = aws_api_gateway_resource.alerts.id
  http_method             = aws_api_gateway_method.get_alerts.http_method
  integration_http_method = "POST"
  type                    = "AWS_PROXY"
  uri                     = aws_lambda_function.api.invoke_arn
}

resource "aws_api_gateway_integration" "delete_alert" {
  rest_api_id             = aws_api_gateway_rest_api.alerts.id
  resource_id             = aws_api_gateway_resource.alert_id.id
  http_method             = aws_api_gateway_method.delete_alert.http_method
  integration_http_method = "POST"
  type                    = "AWS_PROXY"
  uri                     = aws_lambda_function.api.invoke_arn
}

resource "aws_api_gateway_integration" "post_rearm" {
  rest_api_id             = aws_api_gateway_rest_api.alerts.id
  resource_id             = aws_api_gateway_resource.rearm.id
  http_method             = aws_api_gateway_method.post_rearm.http_method
  integration_http_method = "POST"
  type                    = "AWS_PROXY"
  uri                     = aws_lambda_function.api.invoke_arn
}

resource "aws_lambda_permission" "apigw_invoke" {
  statement_id  = "AllowExecutionFromAPIGateway"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.api.function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_api_gateway_rest_api.alerts.execution_arn}/*/*"
}

# ---- Deployment + stage ----
resource "aws_api_gateway_deployment" "alerts" {
  rest_api_id = aws_api_gateway_rest_api.alerts.id

  triggers = {
    redeploy = sha1(jsonencode([
      aws_api_gateway_resource.alerts.id,
      aws_api_gateway_resource.alert_id.id,
      aws_api_gateway_resource.rearm.id,
      aws_api_gateway_method.post_alerts.id,
      aws_api_gateway_method.get_alerts.id,
      aws_api_gateway_method.delete_alert.id,
      aws_api_gateway_method.post_rearm.id,
      aws_api_gateway_integration.post_alerts.id,
      aws_api_gateway_integration.get_alerts.id,
      aws_api_gateway_integration.delete_alert.id,
      aws_api_gateway_integration.post_rearm.id,
    ]))
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_api_gateway_stage" "v1" {
  rest_api_id   = aws_api_gateway_rest_api.alerts.id
  deployment_id = aws_api_gateway_deployment.alerts.id
  stage_name    = "v1"
}

# ---- API key + usage plan (DoW guard: quota + throttle) ----
resource "aws_api_gateway_api_key" "demo" {
  name = "${var.project}-demo-key"
}

resource "aws_api_gateway_usage_plan" "demo" {
  name = "${var.project}-usage-plan"

  api_stages {
    api_id = aws_api_gateway_rest_api.alerts.id
    stage  = aws_api_gateway_stage.v1.stage_name
  }

  quota_settings {
    limit  = 1000
    period = "DAY"
  }

  throttle_settings {
    burst_limit = 10
    rate_limit  = 5
  }
}

resource "aws_api_gateway_usage_plan_key" "demo" {
  key_id        = aws_api_gateway_api_key.demo.id
  key_type      = "API_KEY"
  usage_plan_id = aws_api_gateway_usage_plan.demo.id
}
