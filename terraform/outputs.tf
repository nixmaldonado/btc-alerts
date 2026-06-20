# terraform/outputs.tf

output "api_invoke_url" {
  description = "Base invoke URL for the Price Alert API."
  value       = aws_api_gateway_stage.v1.invoke_url
}

output "alerts_table_name" {
  description = "DynamoDB table name."
  value       = aws_dynamodb_table.alerts.name
}

output "demo_api_key_id" {
  description = "API key id for the demo usage plan (value retrievable via the AWS console/CLI)."
  value       = aws_api_gateway_api_key.demo.id
}

output "usage_plan_id" {
  description = "Demo usage plan id; the local mint-demo-key script attaches new keys to it."
  value       = aws_api_gateway_usage_plan.demo.id
}
