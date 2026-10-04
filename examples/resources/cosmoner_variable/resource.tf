resource "cosmoner_variable" "log_level" {
  name        = "LOG_LEVEL"
  environment = "staging"
  value       = "debug"
}
